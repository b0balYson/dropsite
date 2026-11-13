package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// connectURLCmd issues a connect URL (beta): the MCP address with a one-time
// connect code inside, for the administrator (ADMIN_EMAIL) or the e-mail
// given. The URL is the only stdout output (dropsite-connect and the
// installer read it from there); notes go to stderr.
//
//	docker compose exec -T control /control connect-url [email]
func connectURLCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 1 {
		return errUsage
	}
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	email := cfg.AdminEmail
	if len(args) == 1 {
		email = args[0]
	}
	if email == "" {
		return fmt.Errorf("connect-url: no e-mail given and ADMIN_EMAIL is empty")
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
	link, u, err := svc.CreateConnectURLForEmail(ctx, email)
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return fmt.Errorf("connect-url: no user with e-mail %s", email)
		}
		return fmt.Errorf("connect-url: %w", err)
	}
	fmt.Fprintf(stderr, "connect URL for %s (beta): paste it into the chat app as the MCP server address; "+
		"it works once, until %s\n", u.Email, link.ExpiresAt.UTC().Format(time.RFC3339))
	_, err = fmt.Fprintln(stdout, link.URL)
	return err
}
