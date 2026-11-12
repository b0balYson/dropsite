package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

const testBase = "pp.test"

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t     *testing.T
	svc   *Service
	repo  *store.DB
	root  string
	clock *fakeClock
	admin Actor
}

func newEnv(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Load(func(k string) (string, bool) {
		v, ok := map[string]string{
			"BASE_DOMAIN":    testBase,
			"ADMIN_EMAIL":    "admin@pp.test",
			"ADMIN_PASSWORD": "admin-password",
			"SITES_DIR":      filepath.Join(dir, "sites"),
			"STATE_DIR":      filepath.Join(dir, "state"),
		}[k]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutate {
		m(cfg)
	}
	for _, d := range []string{cfg.SitesDir, cfg.StateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(cfg.StateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := &fakeClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	svc := New(Deps{Config: cfg, Repo: db, Files: storage.NewFS(cfg.SitesDir), Now: clock.Now, UploadDir: cfg.StateDir})
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := db.GetUserByEmail(ctx, "admin@pp.test")
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, svc: svc, repo: db, root: cfg.SitesDir, clock: clock, admin: Actor{User: admin, IP: "10.0.0.1"}}
}

// user creates an active user through the real invite flow.
func (e *env) user(email string) Actor {
	e.t.Helper()
	ctx := context.Background()
	inv, err := e.svc.CreateUser(ctx, e.admin, email, domain.RoleUser)
	if err != nil {
		e.t.Fatal(err)
	}
	secret := inv.URL[strings.LastIndex(inv.URL, "/")+1:]
	u, err := e.svc.RedeemInvite(ctx, secret, "user-password-1", "10.0.0.2")
	if err != nil {
		e.t.Fatal(err)
	}
	return Actor{User: u, IP: "10.0.0.2"}
}

func zipOf(t *testing.T, files map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

// served reads a file the way Caddy would: through by-host/<host>.
func (e *env) served(host, path string) (string, error) {
	b, err := os.ReadFile(filepath.Join(e.root, "by-host", host, filepath.FromSlash(path)))
	return string(b), err
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	de := domain.AsError(err)
	if de == nil || de.Code != code {
		t.Fatalf("want error %s, got %v", code, err)
	}
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}
