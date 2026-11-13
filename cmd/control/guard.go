package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// Abuse protection from the server command line. The running server picks
// the changes up within seconds; Caddy only checks marker files, so nothing
// is reloaded:
//
//	docker compose exec -T control /control defender [status|on|off|strict on|off]
//	docker compose exec -T control /control bans [list|add <ip> [minutes]|clear <ip>]

// cliService opens the database and a service for a one-shot command.
func cliService(ctx context.Context, stderr io.Writer) (*service.Service, func(), error) {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return nil, nil, err
	}
	db, err := store.Open(ctx, dbPath(cfg))
	if err != nil {
		return nil, nil, err
	}
	svc := service.New(service.Deps{
		Config: cfg, Repo: db, Files: storage.NewFS(cfg.SitesDir),
		Logger:    slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel})),
		UploadDir: uploadDir(cfg),
	})
	return svc, func() { _ = db.Close() }, nil
}

// defenderCmd shows or changes the blocking of AI scrapers and cloud
// ranges. status prints "on" or "off" and, on a second line, "strict" or
// "normal".
func defenderCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 2 {
		return errUsage
	}
	svc, closeDB, err := cliService(ctx, stderr)
	if err != nil {
		return err
	}
	defer closeDB()
	st, err := svc.DefenderState(ctx)
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0 || (len(args) == 1 && args[0] == "status"):
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		st.Enabled = args[0] == "on"
	case len(args) == 2 && args[0] == "strict" && (args[1] == "on" || args[1] == "off"):
		st.Strict = args[1] == "on"
	default:
		return errUsage
	}
	if len(args) > 0 && args[0] != "status" {
		if err := svc.SetDefenderCLI(ctx, st); err != nil {
			return fmt.Errorf("defender: %w", err)
		}
	}
	state, mode := "off", "normal"
	if st.Enabled {
		state = "on"
	}
	if st.Strict {
		mode = "strict"
	}
	_, err = fmt.Fprintf(stdout, "%s\n%s\n", state, mode)
	return err
}

// bansCmd lists, adds or lifts IP bans. list prints one ban per line:
// key, level, reason and the end of the ban (RFC 3339, UTC).
func bansCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	action := "list"
	if len(args) > 0 {
		action, args = args[0], args[1:]
	}
	svc, closeDB, err := cliService(ctx, stderr)
	if err != nil {
		return err
	}
	defer closeDB()
	switch {
	case action == "list" && len(args) == 0:
		bans, err := svc.ActiveBansCLI(ctx)
		if err != nil {
			return err
		}
		for _, b := range bans {
			if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s\t%s\n", b.Key, b.Level, b.Reason,
				b.ExpiresAt.UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	case action == "add" && (len(args) == 1 || len(args) == 2):
		var d time.Duration
		if len(args) == 2 {
			m, err := strconv.Atoi(args[1])
			if err != nil || m < 1 || m > 7*24*60 {
				return fmt.Errorf("bans add: minutes must be 1 to %d", 7*24*60)
			}
			d = time.Duration(m) * time.Minute
		}
		b, err := svc.AddBanCLI(ctx, args[0], d)
		if err != nil {
			return fmt.Errorf("bans add: %w", err)
		}
		_, err = fmt.Fprintf(stdout, "%s\t%d\t%s\n", b.Key, b.Level, b.ExpiresAt.UTC().Format(time.RFC3339))
		return err
	case action == "clear" && len(args) == 1:
		if err := svc.ClearBanCLI(ctx, args[0]); err != nil {
			return fmt.Errorf("bans clear: %w", err)
		}
		fmt.Fprintln(stderr, "ban lifted:", args[0])
		return nil
	}
	return errUsage
}
