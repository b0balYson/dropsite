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
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// Rate limits from the PRD (section 8) plus a token brute-force guard.
const (
	DeploysPerHour      = 30
	FailedLoginsPerIP   = 10
	FailedLoginsWindow  = 15 * time.Minute
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
	tokenLimit   *ratelimit.Window
	hasher       *passwordHasher
	siteLocks    keyedMutex
	settingsMu   sync.RWMutex
	settingsData *effectiveSettings
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
		cfg:         d.Config,
		repo:        d.Repo,
		files:       d.Files,
		log:         log,
		now:         func() time.Time { return now().UTC() },
		extract:     extract,
		uploadDir:   d.UploadDir,
		deployLimit: ratelimit.New(DeploysPerHour, time.Hour).WithClock(now),
		loginLimit:  ratelimit.New(FailedLoginsPerIP, FailedLoginsWindow).WithClock(now),
		tokenLimit:  ratelimit.New(FailedTokensPerIP, FailedLoginsWindow).WithClock(now),
		hasher:      newPasswordHasher(),
	}
}

// Actor is the authenticated principal performing an action.
type Actor struct {
	User domain.User
	IP   string
}

// IsAdmin reports whether the actor is an administrator.
func (a Actor) IsAdmin() bool { return a.User.IsAdmin() }

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
