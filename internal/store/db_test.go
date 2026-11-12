package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenMigratesEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "tokens", "sessions", "invites", "sites", "hostnames", "deploys", "audit_log", "settings"} {
		var n int
		if err := db.Reader().QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	var mode string
	if err := db.Writer().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening is idempotent.
	db, err = Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.Reader().QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil || v != LatestVersion() {
		t.Fatalf("version %d, %v", v, err)
	}
	_ = db.Close()
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, "INSERT INTO schema_migrations VALUES (9999, 0)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(ctx, file); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("want ErrSchemaTooNew, got %v", err)
	}
}

// TestOpenAppliesSkippedMigration covers a migration merged with a lower
// number after a higher one already ran on an installation.
func TestOpenAppliesSkippedMigration(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"DROP TABLE form_deliveries", "DROP TABLE form_submissions", "DROP TABLE form_channels",
		"DROP TABLE form_site_settings", "DROP TABLE form_altcha_used", "DELETE FROM schema_migrations WHERE version = 9",
	} {
		if _, err := db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	db, err = Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CountFormChannels(ctx, "x"); err != nil {
		t.Fatalf("skipped migration not applied: %v", err)
	}
}

func TestForeignKeysAndConstraints(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w := db.Writer()
	if _, err := w.ExecContext(ctx, "INSERT INTO sites (id, owner_id, title, created_at, updated_at) VALUES ('x', 42, 't', 0, 0)"); err == nil {
		t.Fatal("foreign keys must be enforced")
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO users (email, role, status, created_at, updated_at) VALUES ('A@x.io','user','active',0,0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO users (email, role, status, created_at, updated_at) VALUES ('a@X.io','user','active',0,0)"); err == nil {
		t.Fatal("email must be unique case-insensitively")
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO users (email, role, status, created_at, updated_at) VALUES ('b@x.io','root','active',0,0)"); err == nil {
		t.Fatal("role check must be enforced")
	}
}
