// Package service implements every use case of the control server and is the
// only place that decides whether an actor may do something. HTTP API, UI
// (and, in stage 2, MCP) are thin adapters over it.
package service

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/dnscheck"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// Rate limits from the PRD (section 8) plus a token brute-force guard.
const (
	DeploysPerHour     = 30
	FailedLoginsPerIP  = 10
	FailedLoginsWindow = 15 * time.Minute
	// FailedLoginsPerAccount caps failures per submitted email (any email,
	// existing or not) across all IPs within FailedLoginsWindow.
	FailedLoginsPerAccount = 20
	// LoginBusyRetryAfter is suggested to a client whose previous login
	// attempt from the same IP is still being checked.
	LoginBusyRetryAfter = time.Second
	FailedTokensPerIP   = 30
	SessionTTL          = 14 * 24 * time.Hour
	InviteTTL           = 72 * time.Hour
	DeletedSiteTTL      = 7 * 24 * time.Hour
	touchInterval       = time.Minute
	uploadOverheadBytes = 1 << 20
	uploadPerFileBytes  = 1024
)

// ExtractFunc extracts an uploaded archive into a staging directory.
type ExtractFunc func(ctx context.Context, src, dest string, lim archive.Limits, now time.Time) (archive.Result, error)

// Deps are the collaborators of the service.
type Deps struct {
	Config  *config.Config
	Repo    store.Repository
	Files   storage.Storage
	Logger  *slog.Logger
	Now     func() time.Time
	Extract ExtractFunc
	// UploadDir holds temporary uploads; it must be on the state volume.
	UploadDir string
	// Resolver answers DNS checks of external domains; nil means the system resolver.
	Resolver dnscheck.Resolver
	// FormSender replaces form delivery transports (tests).
	FormSender FormSender
}

// Service implements the use cases.
type Service struct {
	cfg       *config.Config
	repo      store.Repository
	files     storage.Storage
	log       *slog.Logger
	now       func() time.Time
	extract   ExtractFunc
	uploadDir string

	deployLimit  *ratelimit.Window
	loginLimit   *ratelimit.Window
	accountLimit *ratelimit.Window
	tokenLimit   *ratelimit.Window
	codeLimit    *ratelimit.Window
	regLimit     *ratelimit.Window
	loginBusy    keyedTryLock
	hasher       *passwordHasher
	siteLocks    keyedMutex
	settingsMu   sync.RWMutex
	settingsData *effectiveSettings
	dns          *dnscheck.Checker
	ipCache      serverIPCache

	visitorIPLimit   *ratelimit.Window
	visitorSiteLimit *ratelimit.Window
	visitorMu        sync.Mutex
	visitorKeySet    *visitorKeySet
	formState
}

// New builds a Service.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	extract := d.Extract
	if extract == nil {
		extract = archive.Extract
	}
	log := d.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		cfg:          d.Config,
		repo:         d.Repo,
		files:        d.Files,
		log:          log,
		now:          func() time.Time { return now().UTC() },
		extract:      extract,
		uploadDir:    d.UploadDir,
		deployLimit:  ratelimit.New(DeploysPerHour, time.Hour).WithClock(now),
		loginLimit:   ratelimit.New(FailedLoginsPerIP, FailedLoginsWindow).WithClock(now),
		accountLimit: ratelimit.New(FailedLoginsPerAccount, FailedLoginsWindow).WithClock(now),
		tokenLimit:   ratelimit.New(FailedTokensPerIP, FailedLoginsWindow).WithClock(now),
		codeLimit:    ratelimit.New(FailedCodesPerIP, FailedCodesWindow).WithClock(now),
		regLimit:     ratelimit.New(RegistrationsPerIP, RegistrationWindow).WithClock(now),
		hasher:       newPasswordHasher(),
		dns:          dnscheck.New(d.Resolver, dnscheck.DefaultTimeout),

		visitorIPLimit:   ratelimit.New(VisitorFailuresPerIP, VisitorFailuresWindow).WithClock(now),
		visitorSiteLimit: ratelimit.New(VisitorFailuresPerSite, VisitorFailuresWindow).WithClock(now),
		formState:        newFormState(d, now),
	}
}

// Actor is the authenticated principal performing an action.
type Actor struct {
	User domain.User
	IP   string
	// ViaMCP marks an actor authenticated on the MCP endpoint (an OAuth
	// access token or an API token presented to /mcp). An AI assistant acts
	// only on the user's own resources: it never gets administrator powers,
	// so a prompt injection cannot reach other users' sites.
	ViaMCP bool
}

// IsAdmin reports whether the actor may use administrator powers: an
// administrator acting directly, never through an AI assistant.
func (a Actor) IsAdmin() bool { return a.User.IsAdmin() && !a.ViaMCP }

func (a Actor) id() *int64 {
	if a.User.ID == 0 {
		return nil
	}
	id := a.User.ID
	return &id
}

// requireAdmin hides admin functionality from other users behind "not found".
func requireAdmin(a Actor) error {
	if !a.IsAdmin() {
		return domain.ErrNotFound
	}
	return nil
}

// audit appends to the audit log. Failures are logged, never returned: the
// change has already happened and must not be reported as failed.
func (s *Service) audit(ctx context.Context, a Actor, action, target string, meta map[string]any) {
	e := domain.AuditEntry{
		ActorID:   a.id(),
		Action:    action,
		Target:    target,
		Meta:      meta,
		IP:        a.IP,
		CreatedAt: s.now(),
	}
	if err := s.repo.InsertAudit(context.WithoutCancel(ctx), e); err != nil {
		s.log.Error("audit write failed", "action", action, "target", target, "err", err)
	}
}

// SiteURL returns the https URL for a hostname.
func (s *Service) SiteURL(host string) string {
	if s.cfg.SitesHTTPSPort != 443 {
		return "https://" + host + ":" + strconv.Itoa(s.cfg.SitesHTTPSPort) + "/"
	}
	return "https://" + host + "/"
}

// APIURL returns the public base URL of the API host.
func (s *Service) APIURL() string {
	if s.cfg.SitesHTTPSPort != 443 {
		return "https://" + s.cfg.APIHost + ":" + strconv.Itoa(s.cfg.SitesHTTPSPort)
	}
	return "https://" + s.cfg.APIHost
}

// Now is the service clock (UTC), for adapters that show relative times.
func (s *Service) Now() time.Time { return s.now() }

// Config exposes the immutable configuration to adapters.
func (s *Service) Config() *config.Config { return s.cfg }

// Ping checks the database for health checks.
func (s *Service) Ping(ctx context.Context) error { return s.repo.Ping(ctx) }

// keyedMutex serialises operations per key (site ID).
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*refMutex
}

type refMutex struct {
	sync.Mutex
	refs int
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*refMutex{}
	}
	m := k.locks[key]
	if m == nil {
		m = &refMutex{}
		k.locks[key] = m
	}
	m.refs++
	k.mu.Unlock()
	m.Lock()
	return func() {
		m.Unlock()
		k.mu.Lock()
		m.refs--
		if m.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// keyedTryLock is a non-blocking per-key mutex: a second holder for the same
// key is refused instead of queued.
type keyedTryLock struct {
	mu   sync.Mutex
	held map[string]bool
}

// tryAcquire takes key if it is free and returns its release function.
func (k *keyedTryLock) tryAcquire(key string) (func(), bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.held[key] {
		return nil, false
	}
	if k.held == nil {
		k.held = map[string]bool{}
	}
	k.held[key] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			k.mu.Lock()
			delete(k.held, key)
			k.mu.Unlock()
		})
	}, true
}
