package main

import (
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/brand"
)

func TestVersionGap(t *testing.T) {
	tests := []struct {
		name, cli, server, goos string
		want                    string // substring; "" means no hint
	}{
		{"same", "1.2.3", "1.2.3", "linux", ""},
		{"patch apart", "1.2.3", "1.2.9", "linux", ""},
		{"dev CLI", "dev+abc1234", "1.4.0", "linux", ""},
		{"dev server", "1.2.3", "dev+abc1234", "linux", ""},
		{"old server without version", "1.2.3", "", "linux", ""},
		{"CLI behind", "1.2.3", "1.3.0", "darwin", "the server runs 1.3.0, this CLI is 1.2.3; update the CLI: curl -fsSL "},
		{"CLI behind on Windows", "1.2.3", "2.0.0", "windows", "| iex"},
		{"server behind", "1.4.0", "1.2.3", "linux", "the server runs the older 1.2.3; ask the administrator to update the server (dropsite-update)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := versionGap(tc.cli, tc.server, tc.goos)
			if (tc.want == "" && got != "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
				t.Fatalf("versionGap(%q, %q) = %q, want %q", tc.cli, tc.server, got, tc.want)
			}
		})
	}
}

func setCLIVersion(t *testing.T, v string) {
	t.Helper()
	old := brand.Version
	brand.Version = v
	t.Cleanup(func() { brand.Version = old })
}

func TestVersionHintOnLoginAndDeploy(t *testing.T) {
	setCLIVersion(t, "1.2.0")
	e := newTestEnv(t)
	e.srv.serverVersion = "1.3.1"
	r := e.run("login", "--server", e.srv.URL, "--token", goodToken)
	if r.code != 0 || strings.Count(r.stderr, "the server runs 1.3.1, this CLI is 1.2.0") != 1 {
		t.Fatalf("login: %+v", r)
	}
	r = e.run("deploy", siteDir(t, nil))
	if r.code != 0 || strings.Count(r.stderr, "update the CLI") != 1 {
		t.Fatalf("deploy: %+v", r)
	}
}

func TestNoVersionHintForDevBuildOrSameMinor(t *testing.T) {
	for _, tc := range []struct{ cli, server string }{{"dev", "1.3.0"}, {"1.3.0", "1.3.7"}} {
		setCLIVersion(t, tc.cli)
		e := newTestEnv(t)
		e.srv.serverVersion = tc.server
		e.login()
		r := e.run("deploy", siteDir(t, nil))
		if r.code != 0 || strings.Contains(r.stderr, "server runs") {
			t.Fatalf("cli %s server %s: %+v", tc.cli, tc.server, r)
		}
	}
}
