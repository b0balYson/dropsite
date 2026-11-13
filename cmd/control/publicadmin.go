package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// publicAdminCmd shows or changes public access to the admin interface
// (https://admin.<base domain>). The installer passes the owner's answer
// with it, so the choice is never a permanent environment override; the
// running server picks the change up within seconds:
//
//	docker compose exec -T control /control public-admin [status|on|off]
//
// status prints "on" or "off" (the only stdout output); on and off also
// print the new state and the address to stderr.
func publicAdminCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 1 {
		return errUsage
	}
	action := "status"
	if len(args) == 1 {
		action = args[0]
	}
	if action != "status" && action != "on" && action != "off" {
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
	svc := service.New(service.Deps{
		Config: cfg, Repo: db, Files: storage.NewFS(cfg.SitesDir),
		Logger:    slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel})),
		UploadDir: uploadDir(cfg),
	})
	if action != "status" {
		on := action == "on"
		if err := svc.SetPublicAdminCLI(ctx, on); err != nil {
			return fmt.Errorf("public-admin: %w", err)
		}
		if on {
			fmt.Fprintf(stderr, "the admin interface is open to the internet: %s (a potential threat; turn it off with: control public-admin off)\n", svc.PublicAdminURL())
		} else {
			fmt.Fprintln(stderr, "the admin interface is reachable through the SSH tunnel only")
		}
	}
	state := "off"
	if svc.PublicAdminEnabled(ctx) {
		state = "on"
	}
	_, err = fmt.Fprintln(stdout, state)
	return err
}
