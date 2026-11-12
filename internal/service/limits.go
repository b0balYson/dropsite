package service

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

// LimitsFor returns the effective limits of a user: their overrides on top of
// the global settings (admin setting → environment → default).
func (s *Service) LimitsFor(ctx context.Context, userID int64) (domain.Limits, error) {
	global, err := s.Limits(ctx)
	if err != nil {
		return domain.Limits{}, err
	}
	o, err := s.repo.GetUserLimits(ctx, userID)
	if err != nil {
		return domain.Limits{}, fmt.Errorf("load user limits: %w", err)
	}
	return o.Apply(global), nil
}

// UserLimits is the admin view of one user's limits.
type UserLimits struct {
	User      domain.User
	Overrides domain.LimitOverrides
	Effective domain.Limits
	Global    domain.Limits
}

// GetUserLimits returns a user's overrides and effective limits (admin only).
func (s *Service) GetUserLimits(ctx context.Context, a Actor, userID int64) (UserLimits, error) {
	if err := requireAdmin(a); err != nil {
		return UserLimits{}, err
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return UserLimits{}, err
	}
	global, err := s.Limits(ctx)
	if err != nil {
		return UserLimits{}, err
	}
	o, err := s.repo.GetUserLimits(ctx, userID)
	if err != nil {
		return UserLimits{}, fmt.Errorf("load user limits: %w", err)
	}
	return UserLimits{User: u, Overrides: o, Effective: o.Apply(global), Global: global}, nil
}

func validateOverrides(o domain.LimitOverrides, global domain.Limits) error {
	checks := []struct {
		key string
		v   *int64
		min int64
	}{
		{SettingMaxSiteBytes, o.MaxSiteBytes, 1},
		{SettingMaxFileBytes, o.MaxFileBytes, 1},
		{SettingMaxFiles, o.MaxFiles, 1},
		{SettingMaxSitesPerUser, o.MaxSitesPerUser, 1},
		{SettingKeepReleases, o.KeepReleases, config.MinKeepReleases},
	}
	for _, c := range checks {
		if c.v != nil && *c.v < c.min {
			return invalidSetting(c.key, fmt.Sprintf("must be at least %d", c.min))
		}
	}
	site := global.MaxSiteBytes
	if o.MaxSiteBytes != nil {
		site = *o.MaxSiteBytes
	}
	if o.MaxFileBytes != nil && *o.MaxFileBytes > site {
		return invalidSetting(SettingMaxFileBytes, "must not exceed the site size limit")
	}
	return nil
}

// SetUserLimits replaces a user's overrides; nil fields inherit the global
// value (admin only). The change applies to the next request.
func (s *Service) SetUserLimits(ctx context.Context, a Actor, userID int64, o domain.LimitOverrides) (UserLimits, error) {
	if err := requireAdmin(a); err != nil {
		return UserLimits{}, err
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return UserLimits{}, err
	}
	global, err := s.Limits(ctx)
	if err != nil {
		return UserLimits{}, err
	}
	if err := validateOverrides(o, global); err != nil {
		return UserLimits{}, err
	}
	if err := s.repo.SetUserLimits(ctx, userID, o, s.now()); err != nil {
		return UserLimits{}, err
	}
	s.audit(ctx, a, "user.limits", userTarget(userID), map[string]any{"email": u.Email, "limits": overridesMeta(o)})
	return s.GetUserLimits(ctx, a, userID)
}

func overridesMeta(o domain.LimitOverrides) map[string]any {
	out := map[string]any{}
	for k, v := range map[string]*int64{
		SettingMaxSiteBytes: o.MaxSiteBytes, SettingMaxFileBytes: o.MaxFileBytes, SettingMaxFiles: o.MaxFiles,
		SettingMaxSitesPerUser: o.MaxSitesPerUser, SettingKeepReleases: o.KeepReleases,
	} {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// siteOwnerLimits returns the effective limits of the site's owner.
func (s *Service) siteOwnerLimits(ctx context.Context, site domain.Site) (domain.Limits, error) {
	return s.LimitsFor(ctx, site.OwnerID)
}
