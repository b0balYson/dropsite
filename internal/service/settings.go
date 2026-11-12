package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

// Setting keys an administrator may override.
const (
	SettingMaxSiteBytes    = "max_site_bytes"
	SettingMaxFileBytes    = "max_file_bytes"
	SettingMaxFiles        = "max_files"
	SettingMaxSitesPerUser = "max_sites_per_user"
	SettingKeepReleases    = "keep_releases"
	SettingReservedSlugs   = "reserved_slugs"
)

// SettingKeys lists overridable settings in display order.
var SettingKeys = []string{
	SettingMaxSiteBytes, SettingMaxFileBytes, SettingMaxFiles,
	SettingMaxSitesPerUser, SettingKeepReleases, SettingReservedSlugs,
}

type effectiveSettings struct {
	limits   domain.Limits
	reserved domain.ReservedSlugs
	extra    []string // admin-defined reserved slugs
	raw      map[string]string
}

// Settings is the admin view of global settings.
type Settings struct {
	Limits        domain.Limits
	ExtraReserved []string // admin-defined
	EnvReserved   []string // from RESERVED_SLUGS
	Builtin       []string
	Overridden    map[string]bool
}

func (s *Service) settings(ctx context.Context) (*effectiveSettings, error) {
	s.settingsMu.RLock()
	cur := s.settingsData
	s.settingsMu.RUnlock()
	if cur != nil {
		return cur, nil
	}
	raw, err := s.repo.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	eff := s.buildSettings(raw)
	s.settingsMu.Lock()
	s.settingsData = eff
	s.settingsMu.Unlock()
	return eff, nil
}

func (s *Service) buildSettings(raw map[string]string) *effectiveSettings {
	l := s.cfg.Limits
	if v, ok := parsePositive(raw[SettingMaxSiteBytes]); ok {
		l.MaxSiteBytes = v
	}
	if v, ok := parsePositive(raw[SettingMaxFileBytes]); ok {
		l.MaxFileBytes = v
	}
	if v, ok := parsePositive(raw[SettingMaxFiles]); ok {
		l.MaxFiles = int(v)
	}
	if v, ok := parsePositive(raw[SettingMaxSitesPerUser]); ok {
		l.MaxSitesPerUser = int(v)
	}
	if v, ok := parsePositive(raw[SettingKeepReleases]); ok && v >= config.MinKeepReleases {
		l.KeepReleases = int(v)
	}
	extra := splitSlugs(raw[SettingReservedSlugs])
	return &effectiveSettings{
		limits:   l,
		reserved: domain.NewReservedSlugs(s.cfg.BaseDomain, s.cfg.APIHost, s.cfg.ReservedSlugs, extra),
		extra:    extra,
		raw:      raw,
	}
}

func parsePositive(v string) (int64, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n > 0
}

func splitSlugs(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Limits returns the effective limits for a user.
func (s *Service) Limits(ctx context.Context) (domain.Limits, error) {
	eff, err := s.settings(ctx)
	if err != nil {
		return domain.Limits{}, err
	}
	return eff.limits, nil
}

// GetSettings returns global settings (admin only).
func (s *Service) GetSettings(ctx context.Context, a Actor) (Settings, error) {
	if err := requireAdmin(a); err != nil {
		return Settings{}, err
	}
	eff, err := s.settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	over := map[string]bool{}
	for k := range eff.raw {
		over[k] = true
	}
	return Settings{
		Limits:        eff.limits,
		ExtraReserved: eff.extra,
		EnvReserved:   s.cfg.ReservedSlugs,
		Builtin:       domain.BuiltinReservedSlugs,
		Overridden:    over,
	}, nil
}

// SettingsUpdate carries new values; a nil field leaves the setting as is and
// Reset lists keys to return to the environment default.
type SettingsUpdate struct {
	MaxSiteBytes    *int64
	MaxFileBytes    *int64
	MaxFiles        *int64
	MaxSitesPerUser *int64
	KeepReleases    *int64
	ReservedSlugs   *[]string
	Reset           []string
}

func invalidSetting(key, msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeSettingInvalid, key+": "+msg).
		WithParams(map[string]any{"key": key})
}

func (u SettingsUpdate) values() (map[string]string, error) {
	out := map[string]string{}
	ints := []struct {
		key string
		v   *int64
		min int64
	}{
		{SettingMaxSiteBytes, u.MaxSiteBytes, 1},
		{SettingMaxFileBytes, u.MaxFileBytes, 1},
		{SettingMaxFiles, u.MaxFiles, 1},
		{SettingMaxSitesPerUser, u.MaxSitesPerUser, 1},
		{SettingKeepReleases, u.KeepReleases, config.MinKeepReleases},
	}
	for _, f := range ints {
		if f.v == nil {
			continue
		}
		if *f.v < f.min {
			return nil, invalidSetting(f.key, fmt.Sprintf("must be at least %d", f.min))
		}
		out[f.key] = strconv.FormatInt(*f.v, 10)
	}
	if u.ReservedSlugs != nil {
		list := splitSlugs(strings.Join(*u.ReservedSlugs, ","))
		for _, s := range list {
			if !domain.IsValidHostname(s) || strings.Contains(s, ".") {
				return nil, invalidSetting(SettingReservedSlugs, fmt.Sprintf("%q is not a valid slug", s))
			}
		}
		out[SettingReservedSlugs] = strings.Join(list, ",")
	}
	return out, nil
}

// UpdateSettings validates and stores overrides; they apply immediately.
func (s *Service) UpdateSettings(ctx context.Context, a Actor, u SettingsUpdate) (Settings, error) {
	if err := requireAdmin(a); err != nil {
		return Settings{}, err
	}
	vals, err := u.values()
	if err != nil {
		return Settings{}, err
	}
	cur, err := s.settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	merged := map[string]string{}
	for k, v := range cur.raw {
		merged[k] = v
	}
	for k, v := range vals {
		merged[k] = v
	}
	for _, k := range u.Reset {
		delete(merged, k)
	}
	next := s.buildSettings(merged)
	if next.limits.MaxFileBytes > next.limits.MaxSiteBytes {
		return Settings{}, invalidSetting(SettingMaxFileBytes, "must not exceed the site size limit")
	}
	for k, v := range vals {
		if err := s.repo.SetSetting(ctx, k, v); err != nil {
			return Settings{}, fmt.Errorf("save setting %s: %w", k, err)
		}
	}
	for _, k := range u.Reset {
		if err := s.repo.DeleteSetting(ctx, k); err != nil {
			return Settings{}, fmt.Errorf("reset setting %s: %w", k, err)
		}
	}
	s.settingsMu.Lock()
	s.settingsData = nil
	s.settingsMu.Unlock()
	s.audit(ctx, a, "settings.update", "settings", map[string]any{"set": vals, "reset": u.Reset})
	return s.GetSettings(ctx, a)
}
