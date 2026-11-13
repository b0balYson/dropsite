package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// passwordAlphabet has no look-alike characters (0/O, 1/l/I), so a password
// read off a terminal can be typed back without mistakes.
const (
	passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	passwordLength   = 20
)

// generatePassword returns a random password of passwordLength characters
// (about 116 bits).
func generatePassword() (string, error) {
	out := make([]byte, passwordLength)
	buf := make([]byte, 1)
	limit := 256 - 256%len(passwordAlphabet) // rejection sampling: no modulo bias
	for i := 0; i < len(out); {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		if int(buf[0]) >= limit {
			continue
		}
		out[i] = passwordAlphabet[int(buf[0])%len(passwordAlphabet)]
		i++
	}
	return string(out), nil
}

// resetPasswordCmd sets a new random password for a user and prints it on
// stdout (the only stdout output: scripts such as dropsite-admin-password
// read it from there). Logs and notes go to stderr. Without an argument it
// resets ADMIN_EMAIL:
//
//	docker compose exec -T control /control reset-password [email]
func resetPasswordCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
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
		return fmt.Errorf("reset-password: no e-mail given and ADMIN_EMAIL is empty")
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
	password, err := generatePassword()
	if err != nil {
		return err
	}
	u, err := svc.ResetPassword(ctx, email, password)
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return fmt.Errorf("reset-password: no user with e-mail %s", email)
		}
		return fmt.Errorf("reset-password: %w", err)
	}
	if u.Status == domain.UserBlocked {
		fmt.Fprintf(stderr, "warning: %s is blocked; unblock the account in the web interface\n", u.Email)
	}
	fmt.Fprintf(stderr, "new password set for %s; every session of this user has ended\n", u.Email)
	_, err = fmt.Fprintln(stdout, password)
	return err
}
