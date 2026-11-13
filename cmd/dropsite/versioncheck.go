package main

import (
	"context"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/semver"
)

// versionProbeTimeout bounds the extra /me request made only to learn the
// server version; the command never waits longer for it.
const versionProbeTimeout = 5 * time.Second

const (
	installShURL  = "https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh"
	installPs1URL = "https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1"
)

// versionHint prints one line on stderr when this CLI and the server are a
// minor version or more apart. Development builds and servers that do not
// report a version are never compared. It never fails a command and speaks
// at most once per process (the MCP server publishes many times).
func (a *app) versionHint(serverVersion string) {
	msg := versionGap(brand.Version, serverVersion, a.goos)
	if msg == "" {
		return
	}
	a.hintOnce.Do(func() { fmt.Fprintf(a.stderr, "%s: %s\n", brand.Name, msg) })
}

// probeServerVersion asks the server for its version when the command had no
// reason to call /me itself. Errors are ignored: the hint is optional.
func (a *app) probeServerVersion(ctx context.Context, c *client.Client) {
	if _, ok := semver.Parse(brand.Version); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	if me, err := c.Me(ctx); err == nil {
		a.versionHint(me.ServerVersion)
	}
}

// versionGap returns the hint for a CLI at cli talking to a server at
// server, or "" when there is nothing worth saying.
func versionGap(cli, server, goos string) string {
	c, okC := semver.Parse(cli)
	s, okS := semver.Parse(server)
	if !okC || !okS || !semver.MinorApart(c, s) {
		return ""
	}
	if semver.Compare(c, s) < 0 {
		return fmt.Sprintf("the server runs %s, this CLI is %s; update the CLI: %s", s, c, updateCommand(goos))
	}
	return fmt.Sprintf("this CLI is %s, the server runs the older %s; ask the administrator to update the server (%s-update)", c, s, brand.Name)
}

func updateCommand(goos string) string {
	if goos == "windows" {
		return "irm " + installPs1URL + " | iex"
	}
	return "curl -fsSL " + installShURL + " | sh"
}
