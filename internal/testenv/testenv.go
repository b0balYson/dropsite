// Package testenv builds a real *service.Service on temporary directories for
// adapter tests (api, ui). Adapters may not import store or storage, so the
// wiring lives here; this package is meant for tests only.
//
// Typical use:
//
//	env := testenv.New(t)
//	alice := env.User("alice@pp.test")
//	token := env.Token(alice, "ci")
//	site, _ := env.Svc.CreateSite(ctx, alice, "Promo", "")
//	_, _ = env.Svc.Deploy(ctx, alice, site.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "hi"})), domain.SourceAPI)
package testenv

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// Fixed values of every environment.
const (
	BaseDomain    = "pp.test"
	AdminEmail    = "admin@pp.test"
	AdminPassword = "admin-password"
	// UserPassword is the password of users created by Env.User.
	UserPassword = "user-password-1"
	// AdminIP and UserIP are the IPs recorded on actors returned by Env.
	AdminIP = "10.0.0.1"
	UserIP  = "10.0.0.2"
)

// Start is the initial time of Clock.
var Start = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// Clock is a manual clock shared with the service. It is safe for concurrent use.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// Env is a running service on temporary directories.
type Env struct {
	T      testing.TB
	Svc    *service.Service
	Config *config.Config
	Clock  *Clock
	// Admin is the bootstrap administrator.
	Admin service.Actor
	// SitesDir is the sites volume root (contains by-host/), StateDir holds
	// the database and uploads.
	SitesDir string
	StateDir string
	// DNS answers the service's DNS checks; tests add records with Set.
	DNS *StaticResolver

	repo *store.DB
}

// Option customises New.
type Option func(*options)

type options struct {
	env    map[string]string
	mutate []func(*config.Config)
	sender service.FormSender
}

// WithFormSender replaces the transports used for form delivery.
func WithFormSender(s service.FormSender) Option {
	return func(o *options) { o.sender = s }
}

// WithEnv sets an environment variable read by config.Load.
func WithEnv(key, value string) Option {
	return func(o *options) { o.env[key] = value }
}

// WithConfig mutates the loaded configuration before the service is built.
func WithConfig(f func(*config.Config)) Option {
	return func(o *options) { o.mutate = append(o.mutate, f) }
}

// New starts a service (migrations, bootstrap admin, reconcile) and closes
// it when the test ends.
func New(t testing.TB, opts ...Option) *Env {
	t.Helper()
	dir := t.TempDir()
	o := &options{env: map[string]string{
		"BASE_DOMAIN":    BaseDomain,
		"ADMIN_EMAIL":    AdminEmail,
		"ADMIN_PASSWORD": AdminPassword,
		"SITES_DIR":      filepath.Join(dir, "sites"),
		"STATE_DIR":      filepath.Join(dir, "state"),
	}}
	for _, opt := range opts {
		opt(o)
	}
	cfg := loadConfig(t, o)
	return start(t, cfg, o.sender)
}

func loadConfig(t testing.TB, o *options) *config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) {
		v, ok := o.env[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("testenv: load config: %v", err)
	}
	for _, m := range o.mutate {
		m(cfg)
	}
	return cfg
}

func start(t testing.TB, cfg *config.Config, sender service.FormSender) *Env {
	t.Helper()
	ctx := context.Background()
	uploads := filepath.Join(cfg.StateDir, "uploads")
	for _, d := range []string{cfg.SitesDir, cfg.StateDir, uploads} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("testenv: %v", err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(cfg.StateDir, "state.db"))
	if err != nil {
		t.Fatalf("testenv: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := &Clock{t: Start}
	dns := NewStaticResolver()
	dns.Set(cfg.BaseDomain, ServerIP)
	dns.Set(cfg.APIHost, ServerIP)
	svc := service.New(service.Deps{
		Config: cfg, Repo: db, Files: storage.NewFS(cfg.SitesDir), Now: clock.Now, UploadDir: uploads,
		Resolver:   dns,
		FormSender: sender,
	})
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("testenv: start service: %v", err)
	}
	admin, err := db.GetUserByEmail(ctx, cfg.AdminEmail)
	if err != nil {
		t.Fatalf("testenv: load admin: %v", err)
	}
	return &Env{
		T: t, Svc: svc, Config: cfg, Clock: clock,
		Admin:    service.Actor{User: admin, IP: AdminIP},
		SitesDir: cfg.SitesDir, StateDir: cfg.StateDir, DNS: dns, repo: db,
	}
}

// User creates an active user with role user through the real invite flow.
// The password is UserPassword.
func (e *Env) User(email string) service.Actor {
	return e.userWithRole(email, domain.RoleUser)
}

// AdminUser creates a second active administrator.
func (e *Env) AdminUser(email string) service.Actor {
	return e.userWithRole(email, domain.RoleAdmin)
}

func (e *Env) userWithRole(email string, role domain.Role) service.Actor {
	e.T.Helper()
	ctx := context.Background()
	inv, err := e.Svc.CreateUser(ctx, e.Admin, email, role)
	if err != nil {
		e.T.Fatalf("testenv: create user: %v", err)
	}
	u, err := e.Svc.RedeemInvite(ctx, InviteSecret(inv.URL), UserPassword, UserIP)
	if err != nil {
		e.T.Fatalf("testenv: redeem invite: %v", err)
	}
	return service.Actor{User: u, IP: UserIP}
}

// InviteSecret extracts the secret from an invite URL.
func InviteSecret(url string) string { return url[strings.LastIndex(url, "/")+1:] }

// Token issues a non-expiring API token for a and returns its value.
func (e *Env) Token(a service.Actor, name string) string {
	e.T.Helper()
	nt, err := e.Svc.CreateToken(context.Background(), a, name, 0)
	if err != nil {
		e.T.Fatalf("testenv: create token: %v", err)
	}
	return nt.Value
}

// Login opens a UI session for a user created by User (or the admin) and
// returns the cookie secret.
func (e *Env) Login(email, password string) string {
	e.T.Helper()
	res, err := e.Svc.Login(context.Background(), email, password, UserIP, "testenv")
	if err != nil {
		e.T.Fatalf("testenv: login: %v", err)
	}
	return res.Secret
}

// Served reads a published file the way Caddy would: through by-host/<host>.
func (e *Env) Served(host, path string) (string, error) {
	b, err := os.ReadFile(filepath.Join(e.SitesDir, "by-host", host, filepath.FromSlash(path))) //nolint:gosec // test helper reading the temporary sites volume
	return string(b), err
}

// Zip builds a zip archive from name -> content, in name order.
func Zip(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range sortedKeys(files) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("testenv: zip: %v", err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatalf("testenv: zip: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("testenv: zip: %v", err)
	}
	return buf.Bytes()
}

// TarGz builds a tar.gz archive of regular files from name -> content.
func TarGz(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		body := files[name]
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, ModTime: Start}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("testenv: tar: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("testenv: tar: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("testenv: tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("testenv: gzip: %v", err)
	}
	return buf.Bytes()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
