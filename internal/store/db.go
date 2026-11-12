// Package store persists control state in SQLite.
//
// Writes go through a single connection so SQLite never returns SQLITE_BUSY
// between our own writers; reads use a separate pool (WAL allows concurrent
// readers alongside the writer).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew is returned when the database was migrated by a newer binary.
var ErrSchemaTooNew = errors.New("database schema is newer than this binary; refusing to start (downgrades are not supported)")

// DB is the SQLite database with separate write and read handles.
type DB struct {
	w *sql.DB
	r *sql.DB
}

const busyTimeoutMS = 5000

func dsn(file string, readOnly bool) string {
	q := url.Values{}
	for _, p := range []string{
		"busy_timeout(" + strconv.Itoa(busyTimeoutMS) + ")",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
	} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	if readOnly {
		q.Set("mode", "ro")
	}
	return "file:" + file + "?" + q.Encode()
}

// Open opens (creating if needed) the database file and applies migrations.
func Open(ctx context.Context, file string) (*DB, error) {
	w, err := sql.Open("sqlite", dsn(file, false))
	if err != nil {
		return nil, fmt.Errorf("open database for writing: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetConnMaxLifetime(0)
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open database %s: %w", file, err)
	}
	db := &DB{w: w}
	if err := db.migrate(ctx); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(file, true))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open database for reading: %w", err)
	}
	r.SetMaxOpenConns(8)
	db.r = r
	return db, nil
}

// Close closes both handles.
func (db *DB) Close() error {
	return errors.Join(db.r.Close(), db.w.Close())
}

// Ping checks that the database answers queries (used by /healthz).
func (db *DB) Ping(ctx context.Context) error {
	var one int
	if err := db.r.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Writer and Reader expose the raw handles to repository code in this package
// and to the backup package (VACUUM INTO).
func (db *DB) Writer() *sql.DB { return db.w }
func (db *DB) Reader() *sql.DB { return db.r }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: name must be NNNN_description.sql", e.Name())
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad version: %w", e.Name(), err)
		}
		body, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// LatestVersion is the newest migration version embedded in this binary.
func LatestVersion() int {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

func (db *DB) migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	if _, err := db.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	if err := db.w.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if latest := ms[len(ms)-1].version; current > latest {
		return fmt.Errorf("%w (database %d, binary %d)", ErrSchemaTooNew, current, latest)
	}
	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return err
	}
	// Every missing version is applied, not only those above the newest one:
	// changes developed in parallel may land with a lower number after a
	// higher one has already run on an installation.
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		if err := db.apply(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := db.w.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("read applied migrations: %w", err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return out, nil
}

func (db *DB) apply(ctx context.Context, m migration) error {
	tx, err := db.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		m.version, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}
