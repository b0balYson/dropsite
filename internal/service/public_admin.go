package service

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Public access to the admin interface (change public-admin): the same UI
// as on the loopback listener, served to the internet by Caddy at
// https://admin.<base domain> through a separate control listener. It is off
// unless an administrator turns it on; the database is the source of truth,
// Caddy is never reloaded.
const (
	// SettingPublicAdmin is "1" while public access is on; absent otherwise.
	SettingPublicAdmin = "admin.public"
	// PublicAdminLabel is the label of the public admin host under the base
	// domain. The slug "admin" is reserved, so no site can take it.
	PublicAdminLabel = "admin"
	// publicAdminTTL bounds how long a change made by another process (the
	// "control public-admin" command) takes to reach the running server.
	publicAdminTTL = 2 * time.Second
)

// publicAdminCache remembers the setting for publicAdminTTL: the public
// listener asks for it on every request.
type publicAdminCache struct {
	mu      sync.Mutex
	value   bool
	checked time.Time
}

// PublicAdminHost is the host name of the public admin interface.
func (s *Service) PublicAdminHost() string { return PublicAdminLabel + "." + s.cfg.BaseDomain }

// PublicAdminURL is the address people open, with the HTTPS port when it is
// not 443 (local development).
func (s *Service) PublicAdminURL() string {
	u := "https://" + s.PublicAdminHost()
	if p := s.cfg.SitesHTTPSPort; p != 0 && p != 443 {
		u += ":" + strconv.Itoa(p)
	}
	return u
}

// PublicAdminEnabled reports whether the admin interface is open to the
// internet. Errors count as "off": the public listener then answers 404.
func (s *Service) PublicAdminEnabled(ctx context.Context) bool {
	c := &s.publicAdmin
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if !c.checked.IsZero() && now.Sub(c.checked) < publicAdminTTL {
		return c.value
	}
	raw, err := s.repo.GetSettings(ctx)
	if err != nil {
		s.log.Warn("read public admin setting", "err", err)
		return false
	}
	c.value, c.checked = raw[SettingPublicAdmin] == "1", now
	return c.value
}

func (s *Service) forgetPublicAdmin() {
	s.publicAdmin.mu.Lock()
	s.publicAdmin.checked = time.Time{}
	s.publicAdmin.mu.Unlock()
	s.settingsMu.Lock()
	s.settingsData = nil
	s.settingsMu.Unlock()
}

// PublicAdminStatus is what the settings page shows.
type PublicAdminStatus struct {
	Enabled bool
	URL     string
}

// GetPublicAdmin returns the state of public access (administrators only).
func (s *Service) GetPublicAdmin(ctx context.Context, a Actor) (PublicAdminStatus, error) {
	if err := requireAdmin(a); err != nil {
		return PublicAdminStatus{}, err
	}
	return PublicAdminStatus{Enabled: s.PublicAdminEnabled(ctx), URL: s.PublicAdminURL()}, nil
}

// errPublicAdminConfirm asks for the explicit confirmation of the risk.
var errPublicAdminConfirm = domain.E(domain.KindInvalid, domain.CodeInvalidInput,
	"confirm that you understand the risk and enter your password to open the admin interface to the internet")

// SetPublicAdmin turns public access on or off. Only an administrator signed
// in to the web interface may do it: never an API token, an OAuth grant or
// an AI assistant (MCP), so a leaked token or a prompt injection cannot
// expose the admin interface. Turning it on needs the confirmation of the
// risk and the administrator's current password, checked under the login
// rate limits; turning it off needs nothing.
func (s *Service) SetPublicAdmin(ctx context.Context, a Actor, on, confirmed bool, password string) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	if a.TokenID != 0 || a.GrantID != 0 {
		return domain.ErrNotFound
	}
	if on {
		if !confirmed || password == "" {
			return errPublicAdminConfirm
		}
		if err := s.confirmPassword(ctx, a, password); err != nil {
			return err
		}
	}
	if err := s.storePublicAdmin(ctx, on); err != nil {
		return err
	}
	s.audit(ctx, a, "settings.public_admin", "settings", map[string]any{"enabled": on, "url": s.PublicAdminURL()})
	return nil
}

// SetPublicAdminCLI is SetPublicAdmin for the server command line ("control
// public-admin on|off", used by the installer): whoever runs it is root on
// the server already.
func (s *Service) SetPublicAdminCLI(ctx context.Context, on bool) error {
	if err := s.storePublicAdmin(ctx, on); err != nil {
		return err
	}
	s.audit(ctx, Actor{}, "settings.public_admin", "settings",
		map[string]any{"enabled": on, "url": s.PublicAdminURL(), "source": string(domain.SourceCLI)})
	return nil
}

// confirmPassword checks the actor's own password under the login limits
// (per IP and per account), with the same timing as a login.
func (s *Service) confirmPassword(ctx context.Context, a Actor, password string) error {
	att, err := s.beginLogin(a.User.Email, a.IP)
	if err != nil {
		return err
	}
	defer att.release()
	u, ok, err := s.checkCredentials(ctx, a.User.Email, password)
	if err != nil {
		att.cancel()
		return err
	}
	if !ok || u.ID != a.User.ID {
		s.audit(ctx, a, "auth.confirm_failed", userTarget(a.User.ID), nil)
		return domain.ErrBadCredentials
	}
	att.cancel()
	return nil
}

// storePublicAdmin saves the setting and then the certificate permission
// (on), or withdraws the permission first (off), so Caddy never may obtain a
// certificate for the host while the setting is off.
func (s *Service) storePublicAdmin(ctx context.Context, on bool) error {
	defer s.forgetPublicAdmin()
	if on {
		if err := s.repo.SetSetting(ctx, SettingPublicAdmin, "1"); err != nil {
			return fmt.Errorf("save public admin setting: %w", err)
		}
		return s.syncPublicAdminCert(ctx, true)
	}
	if err := s.syncPublicAdminCert(ctx, false); err != nil {
		return err
	}
	if err := s.repo.DeleteSetting(ctx, SettingPublicAdmin); err != nil {
		return fmt.Errorf("reset public admin setting: %w", err)
	}
	return nil
}

// syncPublicAdminCert writes the certificate permission of the public admin
// host (on-demand TLS) to match on.
func (s *Service) syncPublicAdminCert(ctx context.Context, on bool) error {
	var hosts []string
	if on {
		hosts = []string{s.PublicAdminHost()}
	}
	if err := s.files.SetCertHosts(ctx, hosts); err != nil {
		return fmt.Errorf("certificate permission of %s: %w", s.PublicAdminHost(), err)
	}
	return nil
}

// reconcilePublicAdmin brings the certificate permission in line with the
// database at startup.
func (s *Service) reconcilePublicAdmin(ctx context.Context) error {
	raw, err := s.repo.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	on := raw[SettingPublicAdmin] == "1"
	if on {
		s.log.Warn("the admin interface is open to the internet", "url", s.PublicAdminURL())
	}
	return s.syncPublicAdminCert(ctx, on)
}
