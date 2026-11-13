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
	fs := a.newFlagSet("login", "--server <url> (--code <code> | --token <token|->)",
		"Sign in and save the server and an API token to the config file (owner-only\n"+
			"permissions). --code exchanges a one-time connect code from the Connect AI page\n"+
			"for a new token; --token verifies an existing token ('-' reads it from stdin).\n"+
			"Defaults: "+envServer+", "+envCode+" and "+envToken+".")
	server := fs.String("server", "", "server URL, e.g. https://api.example.com (https is assumed)")
	token := fs.String("token", "", "API token, or - to read it from stdin")
	code := fs.String("code", "", "one-time connect code from the Connect AI page")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 0, 0); err != nil {
		return err
	}
	if *code == "" && *token == "" {
		*code = a.getenv(envCode)
	}
	if *code != "" {
		return a.loginWithCode(ctx, *server, *code)
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
	a.versionHint(me.ServerVersion)
	return nil
}

// loginWithCode exchanges a connect code for a new token and saves it.
func (a *app) loginWithCode(ctx context.Context, server, code string) error {
	if server == "" {
		server = a.getenv(envServer)
	}
	if server == "" {
		return usagef("login --code needs --server (or %s)", envServer)
	}
	norm, err := normalizeServer(server)
	if err != nil {
		return err
	}
	res, err := a.newClient(norm, "").ExchangeConnectCode(ctx, strings.TrimSpace(code), a.tokenName())
	if err != nil {
		return codeError(norm, err)
	}
	path, err := a.saveConfig(config{Server: norm, Token: res.Value})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Logged in to %s as %s\n", norm, res.User.Email)
	fmt.Fprintf(a.stderr, "Saved credentials to %s\n", path)
	a.warnEnvOverrides()
	a.probeServerVersion(ctx, a.newClient(norm, res.Value))
	return nil
}

// tokenName names the token after this computer, so the user recognises it
// in the list of keys.
func (a *app) tokenName() string {
	host := ""
	if a.hostname != nil {
		host, _ = a.hostname()
	}
	if host = sanitizeCell(strings.TrimSpace(host)); host == "" {
		return ""
	}
	if r := []rune(host); len(r) > 40 {
		host = string(r[:40])
	}
	return brand.Name + " CLI on " + host
}

func codeError(server string, err error) error {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Code == "connect_code_invalid" {
		return withHint(fmt.Errorf("%s rejected the connect code, nothing was saved: %w", server, err),
			"Codes work once and expire after 10 minutes. Press Connect on the Connect AI page",
			"of the dropsite web interface for a new command.")
	}
	return fmt.Errorf("could not exchange the connect code, nothing was saved: %w", err)
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
