package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
)

func cmdLogin(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("login", "--server <url> --token <token|->",
		"Verify an API token and save the server and token to the config file\n"+
			"(owner-only permissions). Use '--token -' to read the token from stdin.\n"+
			"Defaults: "+envServer+" and "+envToken+".")
	server := fs.String("server", "", "server URL, e.g. https://api.example.com (https is assumed)")
	token := fs.String("token", "", "API token, or - to read it from stdin")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 0, 0); err != nil {
		return err
	}
	cfg, err := a.loginInput(*server, *token)
	if err != nil {
		return err
	}
	me, err := a.newClient(cfg.Server, cfg.Token).Me(ctx)
	if err != nil {
		return loginError(cfg.Server, err)
	}
	path, err := a.saveConfig(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Logged in to %s as %s\n", cfg.Server, me.User.Email)
	fmt.Fprintf(a.stderr, "Saved credentials to %s\n", path)
	a.warnEnvOverrides()
	return nil
}

// loginInput resolves and validates the server and token for login.
func (a *app) loginInput(server, token string) (config, error) {
	if server == "" {
		server = a.getenv(envServer)
	}
	if token == "" {
		token = a.getenv(envToken)
	}
	if server == "" || token == "" {
		return config{}, usagef("login needs --server and --token (or %s and %s)", envServer, envToken)
	}
	norm, err := normalizeServer(server)
	if err != nil {
		return config{}, err
	}
	if token == "-" {
		if token, err = a.readSecret(); err != nil {
			return config{}, err
		}
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return config{}, usagef("empty token")
	}
	return config{Server: norm, Token: token}, nil
}

func (a *app) readSecret() (string, error) {
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("read token from stdin: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func loginError(server string, err error) error {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.IsUnauthorized() {
		return fmt.Errorf("%s rejected the token, nothing was saved: %w", server, err)
	}
	return fmt.Errorf("could not verify the token, nothing was saved: %w", err)
}

func (a *app) warnEnvOverrides() {
	for _, k := range []string{envServer, envToken} {
		if a.getenv(k) != "" {
			fmt.Fprintf(a.stderr, "Note: %s is set and takes precedence over the saved %s config.\n", k, brand.Name)
		}
	}
}
