package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/backup"
	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

func testConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	env := map[string]string{
		"BASE_DOMAIN":    "pp.test",
		"ADMIN_EMAIL":    "admin@pp.test",
		"ADMIN_PASSWORD": "admin-password",
		"SITES_DIR":      filepath.Join(dir, "sites"),
		"STATE_DIR":      filepath.Join(dir, "state"),
	}
	cfg, err := config.Load(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{cfg.SitesDir, cfg.StateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func siteZip(t *testing.T, body string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("index.html")
	_, _ = w.Write([]byte(body))
	_ = zw.Close()
	return bytes.NewReader(buf.Bytes())
}

func adminActor(t *testing.T, a *app) service.Actor {
	t.Helper()
	res, err := a.svc.Login(context.Background(), "admin@pp.test", "admin-password", "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	return service.Actor{User: res.User}
}

func TestHealthz(t *testing.T) {
	ctx := context.Background()
	a, err := build(ctx, testConfig(t, t.TempDir()), quietLog())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.intern)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()
	_ = a.db.Close()
	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("healthz with closed database: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestBuildFailsWithoutAdminOnEmptyDatabase(t *testing.T) {
	cfg := testConfig(t, t.TempDir())
	cfg.AdminPassword = ""
	if _, err := build(context.Background(), cfg, quietLog()); !errors.Is(err, service.ErrBootstrapCredentials) {
		t.Fatalf("want bootstrap error, got %v", err)
	}
}

func TestBackupRestoreCycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	a, err := build(ctx, cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	admin := adminActor(t, a)
	site, err := a.svc.CreateSite(ctx, admin, "Promo", "promo-may")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.Deploy(ctx, admin, site.Site.ID, siteZip(t, "hello"), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := backup.Backup(ctx, a.db, cfg.SitesDir, cfg.StateDir, &archive); err != nil {
		t.Fatal(err)
	}
	_ = a.db.Close()

	if _, err := backup.Restore(ctx, bytes.NewReader(archive.Bytes()), cfg.StateDir, cfg.SitesDir, false); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("restore over data without force must refuse, got %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(cfg.SitesDir, "by-host", "promo-may.pp.test", "index.html")); err != nil || string(b) != "hello" {
		t.Fatal("refused restore must not change anything")
	}

	// Wipe both volumes completely, then restore.
	for _, d := range []string{cfg.SitesDir, cfg.StateDir} {
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := backup.Restore(ctx, bytes.NewReader(archive.Bytes()), cfg.StateDir, cfg.SitesDir, false); err != nil {
		t.Fatal(err)
	}
	a, err = build(ctx, cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	adminActor(t, a)
	for _, h := range []string{"promo-may.pp.test", site.Site.ID + ".pp.test"} {
		b, err := os.ReadFile(filepath.Join(cfg.SitesDir, "by-host", h, "index.html"))
		if err != nil || string(b) != "hello" {
			t.Fatalf("%s after restore: %q %v", h, b, err)
		}
	}
}

func TestRestoreRejectsHostileArchives(t *testing.T) {
	cfg := testConfig(t, t.TempDir())
	for name, data := range map[string][]byte{
		"not gzip": []byte("hello"),
		"empty":    gzipTar(t, nil),
	} {
		if _, err := backup.Restore(context.Background(), bytes.NewReader(data), cfg.StateDir, cfg.SitesDir, true); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestRunDispatch(t *testing.T) {
	ctx := context.Background()
	if err := run(ctx, []string{"nope"}); !errors.Is(err, errUsage) {
		t.Fatal("unknown command")
	}
	if err := run(ctx, []string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"backup"}); !errors.Is(err, errUsage) {
		t.Fatal("backup needs a path")
	}
	if err := run(ctx, []string{"restore"}); !errors.Is(err, errUsage) {
		t.Fatal("restore needs a path")
	}
	t.Setenv("INTERNAL_ADDR", "127.0.0.1:1")
	if err := run(ctx, []string{"healthcheck"}); err == nil {
		t.Fatal("healthcheck against a closed port must fail")
	}
}

func TestServerTimeouts(t *testing.T) {
	srv := newServer(":0", http.NotFoundHandler(), quietLog())
	if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 15*time.Minute ||
		srv.WriteTimeout != 15*time.Minute || srv.IdleTimeout != 2*time.Minute {
		t.Fatalf("timeouts: header %v read %v write %v idle %v",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
}
