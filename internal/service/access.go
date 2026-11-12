package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/storage"
)

// MaxExpiry is how far in the future a site expiry may be set.
const MaxExpiry = 10 * 365 * 24 * time.Hour

// Expiry is a requested expiry change; a nil At removes the expiry.
type Expiry struct {
	At *time.Time
}

var (
	errPasswordRequired = domain.E(domain.KindInvalid, domain.CodeSitePasswordRequired,
		"set a password to protect the site")
	errPasswordWithUnprotect = domain.E(domain.KindInvalid, domain.CodeInvalidInput,
		"a password cannot be sent together with protected: false")
)

// accessPlan is a validated set of access changes for one site.
type accessPlan struct {
	noindex     *bool
	newPassword string // non-empty: protect with (or change to) this password
	unprotect   bool
	expiry      *Expiry
}

func (s *Service) planAccess(site domain.Site, u SiteUpdate) (accessPlan, error) {
	var p accessPlan
	if u.Noindex != nil && *u.Noindex != site.Noindex {
		p.noindex = u.Noindex
	}
	switch {
	case u.Password != nil:
		if u.Protected != nil && !*u.Protected {
			return p, errPasswordWithUnprotect
		}
		if err := domain.ValidatePassword(*u.Password); err != nil {
			return p, err
		}
		p.newPassword = *u.Password
	case u.Protected != nil && *u.Protected && !site.Protected:
		return p, errPasswordRequired
	case u.Protected != nil && !*u.Protected && site.Protected:
		p.unprotect = true
	}
	if u.Expiry != nil {
		if err := s.checkExpiry(u.Expiry.At); err != nil {
			return p, err
		}
		p.expiry = u.Expiry
	}
	return p, nil
}

func (s *Service) checkExpiry(at *time.Time) error {
	if at == nil {
		return nil
	}
	now := s.now()
	if !at.After(now) || at.Sub(now) > MaxExpiry {
		return domain.E(domain.KindInvalid, domain.CodeExpiryInvalid,
			"the expiry must be in the future and at most 10 years ahead").
			WithParams(map[string]any{"max_days": int(MaxExpiry / (24 * time.Hour))})
	}
	return nil
}

func (p accessPlan) empty() bool {
	return p.noindex == nil && p.newPassword == "" && !p.unprotect && p.expiry == nil
}

// applyAccess performs a validated plan under the site lock. Markers are
// written so that a failure half-way leaves the site closed rather than open.
func (s *Service) applyAccess(ctx context.Context, a Actor, siteID string, p accessPlan) error {
	if p.empty() {
		return nil
	}
	unlock := s.siteLocks.lock(siteID)
	defer unlock()
	hosts, err := s.hostnamesOf(ctx, siteID)
	if err != nil {
		return err
	}
	if p.noindex != nil {
		if err := s.setNoindex(ctx, a, siteID, hosts, *p.noindex); err != nil {
			return err
		}
	}
	if p.newPassword != "" {
		if err := s.protect(ctx, a, siteID, hosts, p.newPassword); err != nil {
			return err
		}
	}
	if p.unprotect {
		if err := s.unprotect(ctx, a, siteID, hosts); err != nil {
			return err
		}
	}
	if p.expiry != nil {
		if err := s.repo.SetSiteExpiry(ctx, siteID, p.expiry.At, s.now()); err != nil {
			return err
		}
		meta := map[string]any{"expires_at": nil}
		if p.expiry.At != nil {
			meta["expires_at"] = p.expiry.At.UTC().Format(time.RFC3339)
		}
		s.audit(ctx, a, "site.expiry", siteTarget(siteID), meta)
	}
	return nil
}

// hostnamesOf lists every name the site is served under: its hostnames and,
// in wildcard mode, the previews of its releases.
func (s *Service) hostnamesOf(ctx context.Context, siteID string) ([]string, error) {
	hs, err := s.repo.ListHostnames(ctx, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Hostname)
	}
	previews, err := s.previewHosts(ctx, siteID)
	if err != nil {
		return nil, err
	}
	return append(out, previews...), nil
}

func (s *Service) setFlag(flag storage.HostFlag, hosts []string) error {
	for _, h := range hosts {
		if err := s.files.SetHostFlag(flag, h); err != nil {
			return fmt.Errorf("set %s marker: %w", flag, err)
		}
	}
	return nil
}

func (s *Service) clearFlag(flag storage.HostFlag, hosts []string) error {
	var errs []error
	for _, h := range hosts {
		if err := s.files.ClearHostFlag(flag, h); err != nil {
			errs = append(errs, fmt.Errorf("clear %s marker: %w", flag, err))
		}
	}
	return errors.Join(errs...)
}

// revertFlag undoes markers set before a failed database write.
func (s *Service) revertFlag(flag storage.HostFlag, hosts []string) {
	if err := s.clearFlag(flag, hosts); err != nil {
		s.log.Error("revert flag markers failed; reconciliation repairs them on restart", "flag", flag, "err", err)
	}
}

func (s *Service) setNoindex(ctx context.Context, a Actor, siteID string, hosts []string, on bool) error {
	if on {
		if err := s.setFlag(storage.FlagNoindex, hosts); err != nil {
			return err
		}
		if err := s.repo.SetSiteNoindex(ctx, siteID, true, s.now()); err != nil {
			s.revertFlag(storage.FlagNoindex, hosts)
			return err
		}
	} else {
		if err := s.repo.SetSiteNoindex(ctx, siteID, false, s.now()); err != nil {
			return err
		}
		if err := s.clearFlag(storage.FlagNoindex, hosts); err != nil {
			return err
		}
	}
	s.audit(ctx, a, "site.noindex", siteTarget(siteID), map[string]any{"noindex": on})
	return nil
}

func (s *Service) protect(ctx context.Context, a Actor, siteID string, hosts []string, password string) error {
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return err
	}
	before, err := s.repo.GetSiteAccess(ctx, siteID)
	if err != nil {
		return err
	}
	if err := s.setFlag(storage.FlagProtected, hosts); err != nil {
		return err
	}
	if _, err := s.repo.SetSitePassword(ctx, siteID, hash, s.now()); err != nil {
		if before.PasswordHash == "" {
			s.revertFlag(storage.FlagProtected, hosts)
		}
		return err
	}
	action := "site.protect"
	if before.PasswordHash != "" {
		action = "site.password_change"
	}
	s.audit(ctx, a, action, siteTarget(siteID), nil)
	return nil
}

func (s *Service) unprotect(ctx context.Context, a Actor, siteID string, hosts []string) error {
	if _, err := s.repo.SetSitePassword(ctx, siteID, "", s.now()); err != nil {
		return err
	}
	if err := s.clearFlag(storage.FlagProtected, hosts); err != nil {
		return err
	}
	s.audit(ctx, a, "site.unprotect", siteTarget(siteID), nil)
	return nil
}

// siteFlags lists the markers a site's hostnames must carry and must not.
func siteFlags(site domain.Site) (on, off []storage.HostFlag) {
	for flag, set := range map[storage.HostFlag]bool{storage.FlagNoindex: site.Noindex, storage.FlagProtected: site.Protected} {
		if set {
			on = append(on, flag)
		} else {
			off = append(off, flag)
		}
	}
	return on, off
}

// linkHost points hostname at the site. Markers the site needs are set before
// the link appears and stale ones (left by a previous holder of the name) are
// cleared after it, so the name is never served with fewer restrictions than
// the site has.
func (s *Service) linkHost(ctx context.Context, siteID, hostname string) error {
	return s.withHostFlags(ctx, siteID, hostname, func() error {
		if err := s.files.LinkHost(hostname, siteID); err != nil {
			return fmt.Errorf("link host: %w", err)
		}
		return nil
	})
}

// withHostFlags runs link (which makes hostname serve the site) between
// setting the markers the site needs and clearing the ones it does not.
func (s *Service) withHostFlags(ctx context.Context, siteID, hostname string, link func() error) error {
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		return err
	}
	on, off := siteFlags(site)
	for _, f := range on {
		if err := s.files.SetHostFlag(f, hostname); err != nil {
			return fmt.Errorf("set %s marker: %w", f, err)
		}
	}
	if err := link(); err != nil {
		return err
	}
	for _, f := range off {
		if err := s.files.ClearHostFlag(f, hostname); err != nil {
			return fmt.Errorf("clear %s marker: %w", f, err)
		}
	}
	return nil
}

// unlinkHost removes hostname from serving, then its markers.
func (s *Service) unlinkHost(hostname string) error {
	if err := s.files.UnlinkHost(hostname); err != nil {
		return fmt.Errorf("unlink host: %w", err)
	}
	for _, f := range storage.AllHostFlags {
		if err := s.files.ClearHostFlag(f, hostname); err != nil {
			return fmt.Errorf("clear %s marker: %w", f, err)
		}
	}
	return nil
}
