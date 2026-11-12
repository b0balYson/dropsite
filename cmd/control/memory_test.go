package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/store"
)

const (
	targetUsers   = 1000
	targetSites   = 10000
	idleMemoryMax = 100 << 20
)

// seed fills the database and the sites volume at the PRD target scale.
func seed(t *testing.T, ctx context.Context, cfgDir string) {
	t.Helper()
	cfg := testConfig(t, cfgDir)
	db, err := store.Open(ctx, dbPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	for u := 1; u <= targetUsers; u++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, role, status, created_at, updated_at)
			VALUES (?, ?, 'x', 'user', 'active', ?, ?)`, u+1, fmt.Sprintf("u%d@pp.test", u), now, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := range targetSites {
		id := fmt.Sprintf("s%09d", i)
		owner := i%targetUsers + 2
		dep := fmt.Sprintf("20261112-000000-%06d", i)
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO sites (id, owner_id, title, current_deploy_id, created_at, updated_at) VALUES (?, ?, 'site', ?, ?, ?)`, []any{id, owner, dep, now, now}},
			{`INSERT INTO hostnames (hostname, site_id, kind, created_at) VALUES (?, ?, 'default', ?)`, []any{id + ".pp.test", id, now}},
			{`INSERT INTO deploys (id, site_id, created_by, source, size_bytes, file_count, status, created_at) VALUES (?, ?, ?, 'cli', 10, 1, 'ready', ?)`, []any{dep, id, owner, now}},
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s.q, s.args...); err != nil {
				t.Fatal(err)
			}
		}
		rel := filepath.Join(cfg.SitesDir, "sites", id, "releases", dep)
		if err := os.MkdirAll(rel, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rel, "index.html"), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestIdleMemoryAtTargetScale checks the PRD budget: control idles below
// 100 MB with 10 000 sites and 1000 users. It measures memory obtained from
// the OS minus memory returned to it, which tracks the process RSS.
func TestIdleMemoryAtTargetScale(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 10 000 sites")
	}
	ctx := context.Background()
	dir := t.TempDir()
	seed(t, ctx, dir)

	a, err := build(ctx, testConfig(t, dir), quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	// Exercise the hot paths once, as a running server would.
	srv := httptest.NewServer(a.intern)
	defer srv.Close()
	if err := a.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	debug.FreeOSMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	inUse := ms.Sys - ms.HeapReleased
	t.Logf("idle memory: sys=%d MiB released=%d MiB in use=%d MiB heap=%d MiB",
		ms.Sys>>20, ms.HeapReleased>>20, inUse>>20, ms.HeapInuse>>20)
	if inUse > idleMemoryMax {
		t.Fatalf("idle memory %d MiB exceeds %d MiB", inUse>>20, idleMemoryMax>>20)
	}
	links, _ := os.ReadDir(filepath.Join(a.cfg.SitesDir, "by-host"))
	if len(links) != targetSites {
		t.Fatalf("reconcile created %d host links, want %d", len(links), targetSites)
	}
}
