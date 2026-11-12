package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// app carries every dependency on the outside world so commands are testable.
type app struct {
	stdout     io.Writer
	stderr     io.Writer
	stdin      io.Reader
	getenv     func(string) string
	configDir  func() (string, error)
	isTerminal func() bool
	httpClient *http.Client // nil: client default
}

func newApp() *app {
	return &app{
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		stdin:      os.Stdin,
		getenv:     os.Getenv,
		configDir:  os.UserConfigDir,
		isTerminal: stdinIsTerminal,
	}
}

// stdinIsTerminal detects an interactive stdin without x/term.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

type command struct {
	summary string
	run     func(ctx context.Context, a *app, args []string) error
}

func commands() map[string]command {
	return map[string]command{
		"login":    {"Verify an API token and save the server and token", cmdLogin},
		"deploy":   {"Publish a directory and print the site URL", cmdDeploy},
		"sites":    {"List your sites", cmdSites},
		"deploys":  {"Show the release history of a site", cmdDeploys},
		"rollback": {"Switch a site to a previous release", cmdRollback},
		"delete":   {"Delete a site and all its releases", cmdDelete},
		"version":  {"Print the CLI version", cmdVersion},
	}
}

func (a *app) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.printUsage(a.stderr)
		return exitUsage
	}
	name, rest := args[0], args[1:]
	switch name {
	case "help", "-h", "-help", "--help":
		return a.help(ctx, rest)
	case "-v", "-version", "--version":
		name = "version"
	}
	cmd, ok := commands()[name]
	if !ok {
		fmt.Fprintf(a.stderr, "%s: unknown command %q\n\n", brand.Name, name)
		a.printUsage(a.stderr)
		return exitUsage
	}
	return a.report(cmd.run(ctx, a, rest))
}

func (a *app) help(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.printUsage(a.stdout)
		return exitOK
	}
	cmd, ok := commands()[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "%s: unknown command %q\n", brand.Name, args[0])
		return exitUsage
	}
	return a.report(cmd.run(ctx, a, []string{"-h"}))
}

func (a *app) printUsage(w io.Writer) {
	fmt.Fprintf(w, "%s %s: publish static sites\n\nUsage:\n  %s <command> [flags] [args]\n\nCommands:\n",
		brand.Name, brand.Version, brand.Name)
	cmds := commands()
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-9s %s\n", n, cmds[n].summary)
	}
	fmt.Fprintf(w, "\nEnvironment:\n  %s, %s override the saved login.\n", envServer, envToken)
	fmt.Fprintf(w, "\nRun '%s <command> -h' for the flags of a command.\n", brand.Name)
}

// report prints err for humans and maps it to an exit code.
func (a *app) report(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	var ue *usageError
	if errors.As(err, &ue) {
		if !ue.shown {
			fmt.Fprintf(a.stderr, "%s: %s\n", brand.Name, ue.msg)
		}
		return exitUsage
	}
	msg, hints := describeError(err)
	fmt.Fprintf(a.stderr, "%s: %s\n", brand.Name, msg)
	for _, h := range hints {
		fmt.Fprintf(a.stderr, "  %s\n", h)
	}
	return exitError
}

func (a *app) newClient(server, token string) *client.Client {
	var opts []client.Option
	if ca := a.getenv(envCAFile); ca != "" && a.httpClient == nil {
		opt, err := client.WithRootCAFile(ca)
		if err != nil {
			fmt.Fprintf(a.stderr, "%s: ignoring %s: %v\n", brand.Name, envCAFile, err)
		} else {
			opts = append(opts, opt)
		}
	}
	if a.httpClient != nil {
		opts = append(opts, client.WithHTTPClient(a.httpClient))
	}
	return client.New(server, token, opts...)
}

// sanitizeCell keeps table output on one line per row.
func sanitizeCell(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}
