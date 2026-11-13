// Command control is the dropsite server: API, UI, accounts and deploys.
//
// Subcommands:
//
//	control serve             run the server (default)
//	control healthcheck       probe /healthz on the internal listener (container health)
//	control backup <path|->   write a consistent backup archive
//	control restore <path|-> [--force]  restore from a backup archive
//	control reset-password [email]  set a new random password, print it (default ADMIN_EMAIL)
//	control version           print the build version
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/b0balYson/dropsite/internal/brand"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "control:", err)
		os.Exit(1)
	}
}

var errUsage = errors.New("usage: control [serve|healthcheck|backup <path|->|restore <path|-> [--force]|reset-password [email]|version]")

func run(ctx context.Context, args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(ctx)
	case "healthcheck":
		return healthcheck(ctx)
	case "backup":
		return backupCmd(ctx, args)
	case "restore":
		return restoreCmd(ctx, args)
	case "reset-password":
		return resetPasswordCmd(ctx, args, os.Stdout, os.Stderr)
	case "version", "--version", "-v":
		fmt.Println(brand.Name, "control", brand.Version)
		return nil
	default:
		return errUsage
	}
}
