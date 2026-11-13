package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/semver"
	"github.com/b0balYson/dropsite/internal/updates"
)

// Release check and update requests (change admin-update-check). control
// asks GitHub for the newest release about twice a day and stores the
// answer in the settings table; an administrator signed in to the web
// interface may ask the host helper to install it.
const (
	SettingUpdateLatest    = "update.latest"
	SettingUpdateURL       = "update.url"
	SettingUpdateCheckedAt = "update.checked_at"

	// UpdateCheckInterval is the period of the automatic check; each wait is
	// shifted by up to UpdateCheckJitter either way.
	UpdateCheckInterval = 12 * time.Hour
	UpdateCheckJitter   = time.Hour
	// updateFirstCheck is the delay of the first check after the start,
	// plus up to updateFirstJitter.
	updateFirstCheck  = 30 * time.Second
	updateFirstJitter = 90 * time.Second
	// ManualChecksPerWindow bounds the "Check now" button.
	ManualChecksPerWindow = 5
	ManualCheckWindow     = 10 * time.Minute
	updateCheckKey        = "update-check"
)

// Errors of the update functions.
var (
	ErrUpdateDisabled = domain.E(domain.KindInvalid, domain.CodeUpdateDisabled,
		"the release check is off (UPDATE_CHECK=0) or this is a development build")
	ErrUpdateFailed = domain.E(domain.KindInvalid, domain.CodeUpdateFailed,
		"could not find out the latest release; try again later")
	ErrUpdateUnavailable = domain.E(domain.KindInvalid, domain.CodeUpdateUnavailable,
		"this version is not a newer release; check for updates again")
	ErrUpdateBusy = domain.E(domain.KindConflict, domain.CodeUpdateBusy,
		"an update is already waiting or running")
	ErrUpdateNoHelper = domain.E(domain.KindInvalid, domain.CodeUpdateNoHelper,
		"the update helper is not installed on this server; run dropsite-update on the server")
	errUpdateConfirm = domain.E(domain.KindInvalid, domain.CodeInvalidInput,
		"confirm the update first")
)

// updateState is the update part of Service, created in New.
type updateState struct {
	version    string
	checker    *updates.Checker
	updFiles   updates.Files
	checkLimit *ratelimit.Window
	updMu      sync.Mutex
	lastErr    error
	reqMu      sync.Mutex
}

func newUpdateState(d Deps, now func() time.Time) updateState {
	version := d.Version
	if version == "" {
		version = brand.Version
	}
	var files updates.Files
	var checker *updates.Checker
	if d.Config != nil {
		files = updates.NewFiles(d.Config.StateDir)
		if d.Config.UpdateCheck {
			// The URL passed config validation; a failure leaves the check off.
			checker, _ = updates.NewChecker(d.Config.UpdateCheckURL, d.UpdateTransport)
		}
	}
	return updateState{
		version:    version,
		checker:    checker,
		updFiles:   files,
		checkLimit: ratelimit.New(ManualChecksPerWindow, ManualCheckWindow).WithClock(now),
	}
}

// UpdateInfo is what the "Updates" section of the settings shows.
type UpdateInfo struct {
	Current string
	// Dev is true for a development build: it is never compared.
	Dev bool
	// Enabled is false with UPDATE_CHECK=0.
	Enabled    bool
	Latest     string
	ReleaseURL string
	CheckedAt  time.Time
	// Available: Latest is newer than Current.
	Available bool
	// CheckFailed: the last check since the start did not succeed.
	CheckFailed bool
	// HelperPresent: the host helper ran within the last minutes.
	HelperPresent bool
	// Pending: a request waits for the helper.
	Pending bool
	// Status is the helper's report of the last update, if any.
	Status *updates.Status
	// Active: an update waits or runs now.
	Active bool
}

func (s *Service) currentVersion() (semver.Version, bool) {
	v, ok := semver.Parse(s.version)
	return v, ok && updates.StrictVersion(v.String())
}

// latestKnown returns the stored result of the last successful check.
func (s *Service) latestKnown(ctx context.Context) (semver.Version, string, time.Time, bool) {
	eff, err := s.settings(ctx)
	if err != nil {
		return semver.Version{}, "", time.Time{}, false
	}
	v, ok := semver.Parse(eff.raw[SettingUpdateLatest])
	if !ok || !updates.StrictVersion(v.String()) {
		return semver.Version{}, "", time.Time{}, false
	}
	at, _ := time.Parse(time.RFC3339, eff.raw[SettingUpdateCheckedAt])
	return v, updates.ReleaseURL(v), at, true
}

// AvailableUpdate returns the newer release, if the last check found one.
// It reads only stored settings (cached), so pages may call it freely; the
// UI shows it to administrators only.
func (s *Service) AvailableUpdate(ctx context.Context) (version, releaseURL string, ok bool) {
	if s.checker == nil {
		return "", "", false
	}
	cur, isRelease := s.currentVersion()
	if !isRelease {
		return "", "", false
	}
	latest, u, _, known := s.latestKnown(ctx)
	if !known || semver.Compare(latest, cur) <= 0 {
		return "", "", false
	}
	return latest.String(), u, true
}

// GetUpdateInfo returns the state of updates (administrators only).
func (s *Service) GetUpdateInfo(ctx context.Context, a Actor) (UpdateInfo, error) {
	if err := requireAdmin(a); err != nil {
		return UpdateInfo{}, err
	}
	_, isRelease := s.currentVersion()
	info := UpdateInfo{Current: s.version, Dev: !isRelease, Enabled: s.checker != nil}
	if latest, u, at, ok := s.latestKnown(ctx); ok {
		info.Latest, info.ReleaseURL, info.CheckedAt = latest.String(), u, at
	}
	_, _, info.Available = s.AvailableUpdate(ctx)
	s.updMu.Lock()
	info.CheckFailed = s.lastErr != nil
	s.updMu.Unlock()
	now := s.now()
	info.HelperPresent = s.updFiles.HelperPresent(now)
	info.Pending = s.updFiles.Pending()
	st, ok, err := s.updFiles.Status()
	if err != nil {
		s.log.Warn("read update status", "err", err)
	}
	if ok {
		info.Status = &st
	}
	info.Active = info.Pending || (ok && st.Active(now))
	return info, nil
}

// CheckUpdatesNow runs the release check at once (administrators only,
// rate limited).
func (s *Service) CheckUpdatesNow(ctx context.Context, a Actor) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	return s.refreshUpdates(ctx)
}

// refreshUpdates is the rate-limited check shared by the "Check now"
// button and the check_updates tool.
func (s *Service) refreshUpdates(ctx context.Context) error {
	if _, isRelease := s.currentVersion(); s.checker == nil || !isRelease {
		return ErrUpdateDisabled
	}
	if ok, retry := s.checkLimit.Allow(updateCheckKey); !ok {
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if err := s.checkUpdates(ctx); err != nil {
		return ErrUpdateFailed.WithCause(err)
	}
	return nil
}

// UpdatesSettingsPath is where an administrator updates in the web interface.
const UpdatesSettingsPath = "/admin/settings#updates-h"

// UpdateFacts is what any signed-in user, API client or assistant may know
// about the version: read-only, it can never start an update.
type UpdateFacts struct {
	Current      string
	Latest       string
	Available    bool
	ReleaseURL   string
	CheckedAt    time.Time
	CheckEnabled bool
	// IsAdmin is the user's role, whatever the channel: an administrator
	// talking to an assistant is told where to update.
	IsAdmin bool
	// HelperPresent and PanelURL are filled for administrators only.
	HelperPresent bool
	PanelURL      string
}

// GetUpdateFacts returns the version facts. With refresh an administrator
// (by role, also through an assistant) runs the check first, under the
// limit of the "Check now" button; refresh is ignored for other users.
// Nothing here writes an update request.
func (s *Service) GetUpdateFacts(ctx context.Context, a Actor, refresh bool) (UpdateFacts, error) {
	if a.User.ID == 0 {
		return UpdateFacts{}, domain.ErrUnauthorized
	}
	isAdmin := a.User.IsAdmin()
	if refresh && isAdmin {
		if err := s.refreshUpdates(ctx); err != nil {
			return UpdateFacts{}, err
		}
	}
	_, isRelease := s.currentVersion()
	f := UpdateFacts{Current: s.version, CheckEnabled: s.checker != nil && isRelease, IsAdmin: isAdmin}
	if latest, u, at, ok := s.latestKnown(ctx); ok && f.CheckEnabled {
		f.Latest, f.ReleaseURL, f.CheckedAt = latest.String(), u, at
	}
	_, _, f.Available = s.AvailableUpdate(ctx)
	if isAdmin {
		f.HelperPresent = s.updFiles.HelperPresent(s.now())
		f.PanelURL = s.CabinetURL(ctx) + UpdatesSettingsPath
	}
	return f, nil
}

// checkUpdates asks for the newest release and stores it.
func (s *Service) checkUpdates(ctx context.Context) error {
	rel, err := s.checker.Latest(ctx)
	s.updMu.Lock()
	s.lastErr = err
	s.updMu.Unlock()
	if err != nil {
		s.log.Warn("release check failed", "err", err)
		return err
	}
	vals := []struct{ k, v string }{
		{SettingUpdateLatest, rel.Version.String()},
		{SettingUpdateURL, rel.URL},
		{SettingUpdateCheckedAt, s.now().Format(time.RFC3339)},
	}
	for _, kv := range vals {
		if err := s.repo.SetSetting(ctx, kv.k, kv.v); err != nil {
			return fmt.Errorf("store release check: %w", err)
		}
	}
	s.settingsMu.Lock()
	s.settingsData = nil
	s.settingsMu.Unlock()
	s.log.Info("release check", "latest", rel.Version.String(), "running", s.version)
	return nil
}

// RunUpdateCheck checks for a new release shortly after the start and then
// about every UpdateCheckInterval until ctx ends. It does nothing with
// UPDATE_CHECK=0 or on a development build.
func (s *Service) RunUpdateCheck(ctx context.Context) {
	if s.checker == nil {
		s.log.Info("release check is off (UPDATE_CHECK=0)")
		return
	}
	if _, isRelease := s.currentVersion(); !isRelease {
		s.log.Debug("release check skipped on a development build", "version", s.version)
		return
	}
	wait := updateFirstCheck + rand.N(updateFirstJitter) //nolint:gosec // scheduling jitter
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 2*updates.CallTimeout)
		_ = s.checkUpdates(cctx)
		cancel()
		wait = UpdateCheckInterval - UpdateCheckJitter + rand.N(2*UpdateCheckJitter) //nolint:gosec // scheduling jitter
	}
}

// RequestUpdate asks the host helper to update to version. Only an
// administrator signed in to the web interface may do it: never an API
// token, an OAuth grant or an AI assistant. The version must be the newer
// release the last check found; one request at a time.
func (s *Service) RequestUpdate(ctx context.Context, a Actor, version string, confirmed bool) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	if a.TokenID != 0 || a.GrantID != 0 {
		return domain.ErrNotFound
	}
	if !confirmed {
		return errUpdateConfirm
	}
	latest, _, ok := s.AvailableUpdate(ctx)
	if !ok || version != latest || !updates.StrictVersion(version) {
		return ErrUpdateUnavailable
	}
	target, _ := semver.Parse(version)
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	now := s.now()
	if !s.updFiles.HelperPresent(now) {
		return ErrUpdateNoHelper
	}
	if st, ok, _ := s.updFiles.Status(); s.updFiles.Pending() || (ok && st.Active(now)) {
		return ErrUpdateBusy
	}
	if err := s.updFiles.WriteRequest(target, a.User.ID); err != nil {
		if errors.Is(err, updates.ErrPending) {
			return ErrUpdateBusy
		}
		return err
	}
	s.audit(ctx, a, "update.request", "settings", map[string]any{"from": s.version, "to": version})
	return nil
}
