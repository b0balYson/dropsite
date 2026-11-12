package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/b0balYson/dropsite/internal/backup"
	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/store"
)

// backupCmd writes a backup to a file or, with "-", to stdout:
//
//	docker compose exec -T control /control backup - > backup.tar.gz
func backupCmd(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, dbPath(cfg))
	if err != nil {
		return err
	}
	defer db.Close()
	out, finish, err := openOutput(args[0])
	if err != nil {
		return err
	}
	m, err := backup.Backup(ctx, db, cfg.SitesDir, cfg.StateDir, out)
	if err = errors.Join(err, finish(err == nil)); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	fmt.Fprintf(os.Stderr, "backup written (schema %d, %s)\n", m.SchemaVersion, m.CreatedAt.Format("2006-01-02 15:04:05Z"))
	return nil
}

// openOutput returns the destination and a finisher that closes it and, on
// failure, removes a partially written file.
func openOutput(path string) (io.Writer, func(ok bool) error, error) {
	if path == "-" {
		return os.Stdout, func(bool) error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // operator-chosen output path
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", path, err)
	}
	return f, func(ok bool) error {
		cerr := f.Close()
		if !ok {
			_ = os.Remove(path) //nolint:gosec // removes the file this command just created
		}
		return cerr
	}, nil
}

// restoreCmd restores a backup while the server is stopped:
//
//	docker compose stop control
//	docker compose run --rm -T control restore - --force < backup.tar.gz
//	docker compose start control
func restoreCmd(ctx context.Context, args []string) error {
	force := false
	var src string
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case src == "":
			src = a
		default:
			return errUsage
		}
	}
	if src == "" {
		return errUsage
	}
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	in := io.Reader(os.Stdin)
	if src != "-" {
		f, err := os.Open(src) //nolint:gosec // operator-chosen backup file
		if err != nil {
			return fmt.Errorf("open %s: %w", src, err)
		}
		defer f.Close()
		in = f
	}
	m, err := backup.Restore(ctx, in, cfg.StateDir, cfg.SitesDir, force)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	// Migrate and reconcile the sites volume with the restored database.
	log := newLogger(cfg.LogLevel)
	a, err := build(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("restore: verify restored data: %w", err)
	}
	if err := a.db.Close(); err != nil {
		return fmt.Errorf("restore: close database: %w", err)
	}
	fmt.Fprintf(os.Stderr, "restored backup from %s (schema %d)\n", m.CreatedAt.Format("2006-01-02 15:04:05Z"), m.SchemaVersion)
	return nil
}
