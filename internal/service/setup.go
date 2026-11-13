package service

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/setupcheck"
	"github.com/b0balYson/dropsite/internal/store"
)

// SettingSetupHidden marks the first-run checklist as dismissed.
const SettingSetupHidden = "setup.hidden"

// SetupProgress is the part of the first-run checklist that the database
// answers: cheap enough for every page.
type SetupProgress struct {
	// Chat is true when the administrator has an active app connection or
	// API token.
	Chat bool
	// Site is true when the administrator owns a site.
	Site bool
	// Team is true when the installation has another user.
	Team bool
	// Requests is true when Telegram or e-mail delivery is configured.
	Requests bool
	// OnDemand is true when certificates are issued per name (TLS_MODE).
	OnDemand bool
	Hidden   bool
}

// SetupProgress reports the local checklist steps (admin only).
func (s *Service) SetupProgress(ctx context.Context, a Actor) (SetupProgress, error) {
	if err := requireAdmin(a); err != nil {
		return SetupProgress{}, err
	}
	out := SetupProgress{OnDemand: !s.cfg.Wildcard(), Hidden: s.SetupHidden(ctx)}
	var err error
	if out.Chat, err = s.hasConnection(ctx, a); err != nil {
		return SetupProgress{}, err
	}
	_, _, sites, err := s.Me(ctx, a)
	if err != nil {
		return SetupProgress{}, err
	}
	out.Site = sites > 0
	_, users, err := s.repo.ListUsers(ctx, store.UserFilter{Page: store.Page{Limit: 1}})
	if err != nil {
		return SetupProgress{}, fmt.Errorf("count users: %w", err)
	}
	out.Team = users > 1
	avail, err := s.FormChannelAvailability(ctx)
	if err != nil {
		return SetupProgress{}, err
	}
	out.Requests = avail.Telegram || avail.Email
	return out, nil
}

func (s *Service) hasConnection(ctx context.Context, a Actor) (bool, error) {
	grants, err := s.ListOAuthGrants(ctx, a)
	if err != nil {
		return false, err
	}
	if len(grants) > 0 {
		return true, nil
	}
	tokens, err := s.ListTokens(ctx, a)
	if err != nil {
		return false, err
	}
	now := s.now()
	for _, t := range tokens {
		if t.Active(now) {
			return true, nil
		}
	}
	return false, nil
}

// setupTarget is what the network checks look at.
func (s *Service) setupTarget() setupcheck.Target {
	return setupcheck.Target{BaseDomain: s.cfg.BaseDomain, ServerIPs: s.cfg.ServerIPs, APIURL: s.APIURL()}
}

// SetupChecks runs the DNS and HTTPS checks of the checklist (admin only).
// Reports are cached briefly; force asks for a new check.
func (s *Service) SetupChecks(ctx context.Context, a Actor, force bool) (setupcheck.Report, error) {
	if err := requireAdmin(a); err != nil {
		return setupcheck.Report{}, err
	}
	return s.setup.Run(ctx, s.setupTarget(), force), nil
}

// CachedSetupChecks returns the last check if it is still fresh, without
// touching the network (admin only).
func (s *Service) CachedSetupChecks(a Actor) (setupcheck.Report, bool) {
	if requireAdmin(a) != nil {
		return setupcheck.Report{}, false
	}
	return s.setup.Cached(s.setupTarget())
}

// SetupHidden reports whether an administrator dismissed the checklist.
func (s *Service) SetupHidden(ctx context.Context) bool {
	eff, err := s.settings(ctx)
	if err != nil {
		s.log.Warn("read settings", "err", err)
		return false
	}
	return eff.raw[SettingSetupHidden] == "1"
}

// SetSetupHidden hides or shows the checklist for every administrator.
func (s *Service) SetSetupHidden(ctx context.Context, a Actor, hidden bool) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	var err error
	if hidden {
		err = s.repo.SetSetting(ctx, SettingSetupHidden, "1")
	} else {
		err = s.repo.DeleteSetting(ctx, SettingSetupHidden)
	}
	if err != nil {
		return fmt.Errorf("save checklist visibility: %w", err)
	}
	s.settingsMu.Lock()
	s.settingsData = nil
	s.settingsMu.Unlock()
	action := "setup.show"
	if hidden {
		action = "setup.hide"
	}
	s.audit(ctx, a, action, "settings", nil)
	return nil
}
