package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/guard"
)

// Abuse protection (change abuse-protection): bans of client networks,
// the staged response to bots and the global switches of caddy-defender.
// The database is the source of truth; marker files under the sites volume
// are what Caddy checks, so nothing ever reloads Caddy.
const (
	// banRefresh bounds how long a ban change made by another process (the
	// "control bans" command) takes to reach the running server.
	banRefresh = 5 * time.Second
	// maxBanAddresses bounds the exact-address markers of one IPv6 ban.
	maxBanAddresses = 64
	// GuardInterval is how often the access log is read for violations.
	GuardInterval = 2 * time.Second
	// guardMaxLineAge skips log lines older than this (a reread after
	// rotation must not ban for old traffic).
	guardMaxLineAge = 2 * time.Minute
	guardReadBytes  = 4 << 20
	guardMaxReads   = 16

	// SettingDefenderOff and SettingDefenderStrict are "1" while set.
	SettingDefenderOff    = "defender.off"
	SettingDefenderStrict = "defender.strict"
)

// Ban reasons.
const (
	BanReasonRate     = "rate_limit"
	BanReasonPeriodic = "bot_pattern"
	BanReasonManual   = "manual"
)

// guardState is the abuse-protection part of Service, created in New.
type guardState struct {
	periodic   *guard.Periodicity
	suspicion  *guard.Suspicion
	violations *guard.Violations
	markers    *guard.Markers
	sessions   *sessionUses

	bansMu     sync.Mutex
	bans       map[string]domain.IPBan // active bans by key
	bansLoaded time.Time

	budgetMu  sync.Mutex
	exhausted map[int64]string // endpoint ID → UTC day its budget ran out
}

func newGuardState(d Deps) guardState {
	root := ""
	if d.Config != nil {
		root = d.Config.SitesDir
	}
	return guardState{
		periodic:   guard.NewPeriodicity(0),
		suspicion:  guard.NewSuspicion(),
		violations: guard.NewViolations(),
		markers:    guard.NewMarkers(root),
		sessions:   newSessionUses(),
		exhausted:  map[int64]string{},
	}
}

// loadBans refreshes the active bans from the database at most every
// banRefresh unless force is set.
func (s *Service) loadBans(ctx context.Context, force bool) (map[string]domain.IPBan, error) {
	now := s.now()
	s.bansMu.Lock()
	defer s.bansMu.Unlock()
	if !force && s.bans != nil && now.Sub(s.bansLoaded) < banRefresh {
		return s.bans, nil
	}
	rows, err := s.repo.ListBans(ctx)
	if err != nil {
		return s.bans, err
	}
	active := map[string]domain.IPBan{}
	for _, b := range rows {
		if b.Active(now) {
			active[b.Key] = b
		}
	}
	s.bans, s.bansLoaded = active, now
	return active, nil
}

// netBanned reports whether the client's network is banned and for how
// long. A database error counts as "not banned": Caddy still answers from
// the marker files.
func (s *Service) netBanned(ctx context.Context, ip string) (bool, time.Duration) {
	key := guard.NetKey(ip)
	if key == "" {
		return false, 0
	}
	bans, err := s.loadBans(ctx, false)
	if err != nil {
		s.log.Warn("read bans", "err", err)
	}
	b, ok := bans[key]
	if !ok || !b.Active(s.now()) {
		return false, 0
	}
	return true, b.ExpiresAt.Sub(s.now())
}

// errBanned is the visitor answer for a banned network.
func errBanned(retry time.Duration) error {
	return domain.ErrRateLimited.WithRetryAfter(max(retry, time.Second))
}

// checkNotBanned refuses calls that reach control from a banned network
// (an IPv6 address Caddy has no marker for yet).
func (s *Service) checkNotBanned(ctx context.Context, ip string) error {
	if banned, retry := s.netBanned(ctx, ip); banned {
		return errBanned(retry)
	}
	return nil
}

// banNet bans the network of ip, one step up the ladder when the network
// was banned within guard.BanMemory.
func (s *Service) banNet(ctx context.Context, ip, reason string, dur time.Duration) (domain.IPBan, error) {
	key := guard.NetKey(ip)
	if key == "" {
		return domain.IPBan{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "not an IP address: "+ip)
	}
	now := s.now()
	b := domain.IPBan{Key: key, Level: 1, Reason: reason, CreatedAt: now}
	old, err := s.repo.GetBan(ctx, key)
	switch {
	case err == nil:
		if now.Sub(old.ExpiresAt) < guard.BanMemory {
			b.Level = min(old.Level+1, len(guard.BanLadder))
		}
		b.Addresses = old.Addresses
	case !errors.Is(err, domain.ErrNotFound):
		return domain.IPBan{}, err
	}
	if dur <= 0 {
		dur = guard.BanDuration(b.Level)
	}
	b.ExpiresAt = now.Add(dur)
	if addr := guard.CanonicalIP(ip); addr != "" && !slices.Contains(b.Addresses, addr) && len(b.Addresses) < maxBanAddresses {
		b.Addresses = append(slices.Clone(b.Addresses), addr)
	}
	for _, a := range b.Addresses {
		if err := s.markers.Ban(a); err != nil {
			return domain.IPBan{}, fmt.Errorf("ban marker: %w", err)
		}
	}
	if err := s.repo.PutBan(ctx, b); err != nil {
		return domain.IPBan{}, err
	}
	s.cacheBan(b)
	s.log.Warn("network banned", "key", key, "level", b.Level, "reason", reason, "until", b.ExpiresAt)
	s.audit(ctx, Actor{}, "guard.ban", "ip:"+key, map[string]any{
		"level": b.Level, "reason": reason, "expires_at": b.ExpiresAt.Format(time.RFC3339), "source": "guard",
	})
	return b, nil
}

func (s *Service) cacheBan(b domain.IPBan) {
	s.bansMu.Lock()
	defer s.bansMu.Unlock()
	if s.bans == nil {
		return // loaded on first use
	}
	if b.Active(s.now()) {
		s.bans[b.Key] = b
	} else {
		delete(s.bans, b.Key)
	}
}

// coverBannedAddress adds a marker for an address of a banned IPv6 network
// that Caddy has no marker for yet.
func (s *Service) coverBannedAddress(ctx context.Context, ip string) {
	key := guard.NetKey(ip)
	if !guard.IsNet(key) {
		return
	}
	bans, _ := s.loadBans(ctx, false)
	b, ok := bans[key]
	if !ok || slices.Contains(b.Addresses, ip) || len(b.Addresses) >= maxBanAddresses {
		return
	}
	if err := s.markers.Ban(ip); err != nil {
		s.log.Warn("ban marker", "err", err)
		return
	}
	b.Addresses = append(slices.Clone(b.Addresses), ip)
	if err := s.repo.PutBan(ctx, b); err != nil {
		s.log.Warn("store ban addresses", "err", err)
	}
	s.cacheBan(b)
}

// flagNet raises the suspicion level of the client's network; at
// guard.BanLevel the network is banned.
func (s *Service) flagNet(ctx context.Context, ip, reason string) {
	key := guard.NetKey(ip)
	if key == "" {
		return
	}
	level := s.suspicion.Flag(key, s.now())
	s.log.Info("bot pattern detected", "key", key, "reason", reason, "level", level)
	if level >= guard.BanLevel {
		if _, err := s.banNet(context.WithoutCancel(ctx), ip, BanReasonPeriodic, 0); err != nil {
			s.log.Error("ban network", "key", key, "err", err)
		}
	}
}

// observeCall feeds the periodicity detector with a call of what (an
// endpoint or a form) and reacts to its signal.
func (s *Service) observeCall(ctx context.Context, ip, what string) {
	key := guard.NetKey(ip)
	if key == "" {
		return
	}
	if sig := s.periodic.Observe(key+"|"+what, s.now()); sig != guard.SignalNone {
		s.flagNet(ctx, ip, sig.String()+" "+what)
	}
}

// suspicionLevel returns the current level of the client's network.
func (s *Service) suspicionLevel(ip string) int {
	return s.suspicion.Level(guard.NetKey(ip), s.now())
}

// ActiveBans lists the bans in force (administrators only).
func (s *Service) ActiveBans(ctx context.Context, a Actor) ([]domain.IPBan, error) {
	if err := requireAdmin(a); err != nil {
		return nil, err
	}
	return s.activeBans(ctx)
}

func (s *Service) activeBans(ctx context.Context) ([]domain.IPBan, error) {
	rows, err := s.repo.ListBans(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := []domain.IPBan{}
	for _, b := range rows {
		if b.Active(now) {
			out = append(out, b)
		}
	}
	return out, nil
}

// ClearBan lifts a ban (administrators only); ref is the ban key or any
// address of the banned network.
func (s *Service) ClearBan(ctx context.Context, a Actor, ref string) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	return s.clearBan(ctx, a, ref)
}

// ClearBanCLI is ClearBan for "control bans clear".
func (s *Service) ClearBanCLI(ctx context.Context, ref string) error {
	return s.clearBan(ctx, Actor{}, ref)
}

// AddBanCLI bans the network of ip for d ("control bans add"); d ≤ 0 uses
// the ladder.
func (s *Service) AddBanCLI(ctx context.Context, ip string, d time.Duration) (domain.IPBan, error) {
	b, err := s.banNet(ctx, ip, BanReasonManual, d)
	if err != nil {
		return domain.IPBan{}, err
	}
	return b, nil
}

// ActiveBansCLI lists the bans in force for "control bans list".
func (s *Service) ActiveBansCLI(ctx context.Context) ([]domain.IPBan, error) {
	return s.activeBans(ctx)
}

func banKey(ref string) string {
	if p, err := netip.ParsePrefix(ref); err == nil {
		return p.Masked().String()
	}
	return guard.NetKey(ref)
}

func (s *Service) clearBan(ctx context.Context, a Actor, ref string) error {
	key := banKey(ref)
	if key == "" {
		return domain.ErrNotFound
	}
	b, err := s.repo.GetBan(ctx, key)
	if err != nil {
		return err
	}
	if err := s.removeBanMarkers(b); err != nil {
		return err
	}
	if err := s.repo.DeleteBan(ctx, key); err != nil {
		return err
	}
	b.ExpiresAt = s.now()
	s.cacheBan(b)
	s.suspicion.Forget(key)
	meta := map[string]any{"level": b.Level, "reason": b.Reason}
	if a.User.ID == 0 {
		meta["source"] = string(domain.SourceCLI)
	}
	s.audit(ctx, a, "guard.unban", "ip:"+key, meta)
	return nil
}

func (s *Service) removeBanMarkers(b domain.IPBan) error {
	for _, addr := range b.Addresses {
		if err := s.markers.Unban(addr); err != nil {
			return fmt.Errorf("remove ban marker: %w", err)
		}
	}
	return nil
}

// expireBans removes the markers of bans that ended and forgets rows past
// their memory period.
func (s *Service) expireBans(ctx context.Context) error {
	rows, err := s.repo.ListBans(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, b := range rows {
		if b.Active(now) || len(b.Addresses) == 0 {
			continue
		}
		if err := s.removeBanMarkers(b); err != nil {
			return err
		}
		b.Addresses = nil
		if err := s.repo.PutBan(ctx, b); err != nil {
			return err
		}
	}
	if _, err := s.repo.DeleteBansExpiredBefore(ctx, now.Add(-guard.BanMemory)); err != nil {
		return err
	}
	// Markers without an active ban (left by a crash or a restore).
	active := map[string]bool{}
	for _, b := range rows {
		if b.Active(now) {
			for _, a := range b.Addresses {
				active[a] = true
			}
		}
	}
	addrs, err := s.markers.Banned()
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if !active[a] {
			if err := s.markers.Unban(a); err != nil {
				s.log.Warn("remove stray ban marker", "addr", a, "err", err)
			}
		}
	}
	_, err = s.loadBans(ctx, true)
	return err
}

// DefenderStatus is the state of the scraper and cloud blocking.
type DefenderStatus struct {
	Enabled bool
	Strict  bool
}

// GetDefender returns the defender switches (administrators only).
func (s *Service) GetDefender(ctx context.Context, a Actor) (DefenderStatus, error) {
	if err := requireAdmin(a); err != nil {
		return DefenderStatus{}, err
	}
	return s.DefenderState(ctx)
}

// DefenderState reads the switches from the settings.
func (s *Service) DefenderState(ctx context.Context) (DefenderStatus, error) {
	raw, err := s.repo.GetSettings(ctx)
	if err != nil {
		return DefenderStatus{}, err
	}
	return DefenderStatus{Enabled: raw[SettingDefenderOff] != "1", Strict: raw[SettingDefenderStrict] == "1"}, nil
}

// SetDefender changes the switches (administrators in the web interface).
func (s *Service) SetDefender(ctx context.Context, a Actor, st DefenderStatus) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	if err := s.storeDefender(ctx, st); err != nil {
		return err
	}
	s.audit(ctx, a, "settings.defender", "settings", map[string]any{"enabled": st.Enabled, "strict": st.Strict})
	return nil
}

// SetDefenderCLI is SetDefender for "control defender".
func (s *Service) SetDefenderCLI(ctx context.Context, st DefenderStatus) error {
	if err := s.storeDefender(ctx, st); err != nil {
		return err
	}
	s.audit(ctx, Actor{}, "settings.defender", "settings",
		map[string]any{"enabled": st.Enabled, "strict": st.Strict, "source": string(domain.SourceCLI)})
	return nil
}

func (s *Service) storeDefender(ctx context.Context, st DefenderStatus) error {
	set := func(key string, on bool) error {
		if on {
			return s.repo.SetSetting(ctx, key, "1")
		}
		return s.repo.DeleteSetting(ctx, key)
	}
	if err := set(SettingDefenderOff, !st.Enabled); err != nil {
		return fmt.Errorf("save defender setting: %w", err)
	}
	if err := set(SettingDefenderStrict, st.Strict); err != nil {
		return fmt.Errorf("save defender setting: %w", err)
	}
	return s.syncDefenderMarkers(st)
}

func (s *Service) syncDefenderMarkers(st DefenderStatus) error {
	if err := s.markers.SetFlag(guard.FlagDefenderOff, !st.Enabled); err != nil {
		return err
	}
	return s.markers.SetFlag(guard.FlagDefenderStrict, st.Strict)
}

// reconcileGuard brings the defender markers in line with the database and
// drops ban markers that outlived their ban, at startup.
func (s *Service) reconcileGuard(ctx context.Context) error {
	st, err := s.DefenderState(ctx)
	if err != nil {
		return fmt.Errorf("load defender settings: %w", err)
	}
	if err := s.syncDefenderMarkers(st); err != nil {
		return err
	}
	if !st.Enabled {
		s.log.Warn("blocking of AI scrapers and cloud ranges is off (control defender on)")
	}
	return s.expireBans(ctx)
}
