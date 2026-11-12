package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/brand"
)

func TestSitesTableAndJSON(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Blog\twith tab", "blog", 1)
	e.srv.addSite("Empty", "", 0)
	r := e.run("sites")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "ID", "LAST DEPLOY", "https://blog.sites.test", "Blog with tab", "2026-05-01")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 3 || !strings.HasSuffix(strings.TrimSpace(lines[2]), "-") {
		t.Fatalf("table:\n%s", r.stdout)
	}

	r = e.run("sites", "--json")
	expectCode(t, r, 0)
	var items []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &items); err != nil || len(items) != 2 {
		t.Fatalf("json: %v\n%s", err, r.stdout)
	}
}

func TestSitesEmpty(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	r := e.run("sites")
	expectCode(t, r, 0)
	expectContains(t, "stderr", r.stderr, "No sites yet")
	r = e.run("sites", "--json")
	if strings.TrimSpace(r.stdout) != "[]" {
		t.Fatalf("json = %q", r.stdout)
	}
	expectCode(t, e.run("sites", "x"), 2)
}

func TestDeploysMarksCurrent(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	s := e.srv.addSite("Promo", "promo-may", 2)
	r := e.run("deploys", "promo-may")
	expectCode(t, r, 0)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("table:\n%s", r.stdout)
	}
	if !strings.HasPrefix(lines[1], "*") || !strings.Contains(lines[1], s.Deploys[1].ID) {
		t.Fatalf("newest deploy must be first and current:\n%s", r.stdout)
	}
	if strings.HasPrefix(lines[2], "*") {
		t.Fatalf("only one current:\n%s", r.stdout)
	}

	r = e.run("deploys", "promo-may", "--json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &items); err != nil || items[0]["current"] != true {
		t.Fatalf("json: %v\n%s", err, r.stdout)
	}
}

func TestDeploysEmptyAndMissing(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("New", "fresh", 0)
	r := e.run("deploys", "fresh")
	expectCode(t, r, 0)
	expectContains(t, "stderr", r.stderr, "No deploys yet")
	r = e.run("deploys", "ghost")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "not found (not_found)")
	expectCode(t, e.run("deploys"), 2)
}

func TestRollbackToPrevious(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	s := e.srv.addSite("Promo", "promo-may", 2)
	r := e.run("rollback", "promo-may")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "Switched to deploy "+s.Deploys[0].ID, "https://promo-may.sites.test")
	if e.srv.sites[s.ID].Current != 0 {
		t.Fatal("site was not switched")
	}
}

func TestRollbackToSpecificJSON(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	s := e.srv.addSite("Promo", "promo", 3)
	r := e.run("rollback", "promo", s.Deploys[0].ID, "--json")
	expectCode(t, r, 0)
	var out deployOutput
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.DeployID != s.Deploys[0].ID || out.SiteID != s.ID || out.URL == "" {
		t.Fatalf("out = %+v", out)
	}
}

func TestRollbackWithoutPrevious(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Promo", "promo", 1)
	r := e.run("rollback", "promo")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "no_previous_deploy")
	expectCode(t, e.run("rollback"), 2)
	expectCode(t, e.run("rollback", "a", "b", "c"), 2)
}

func TestDeleteNonInteractiveRefuses(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Promo", "promo", 1)
	r := e.run("delete", "promo")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "without confirmation", "--yes")
	if e.srv.siteCount() != 1 || e.srv.sawRequest("DELETE") {
		t.Fatal("site was deleted")
	}
}

func TestDeleteWithYes(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Promo", "promo", 1)
	r := e.run("delete", "promo", "--yes")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "Deleted site promo")
	if e.srv.siteCount() != 0 {
		t.Fatal("site still exists")
	}
	r = e.run("delete", "promo", "--yes")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "not found")
}

func TestDeleteInteractive(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		code    int
		deleted bool
	}{
		{"slug", "promo\n", 0, true},
		{"id", "site01\n", 0, true},
		{"mismatch", "nope\n", 1, false},
		{"eof", "", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.login()
			e.srv.addSite("Promo", "promo", 1)
			e.terminal = true
			e.stdin = tt.answer
			r := e.run("delete", "promo")
			expectCode(t, r, tt.code)
			expectContains(t, "stderr", r.stderr, "Type the site slug or ID")
			if (e.srv.siteCount() == 0) != tt.deleted {
				t.Fatalf("deleted = %v, want %v", e.srv.siteCount() == 0, tt.deleted)
			}
		})
	}
}

func TestDeleteInteractiveMissingSite(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.terminal = true
	r := e.run("delete", "ghost")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "site ghost")
}

func TestUnauthorizedHint(t *testing.T) {
	e := newTestEnv(t)
	e.env[envServer] = e.srv.URL
	e.env[envToken] = "ds_revoked"
	for _, args := range [][]string{{"sites"}, {"deploys", "x"}, {"rollback", "x"}, {"delete", "x", "--yes"}} {
		r := e.run(args...)
		expectCode(t, r, 1)
		expectContains(t, "stderr", r.stderr, "authentication required (unauthorized)", "dropsite login")
	}
}

func TestNotLoggedInEverywhere(t *testing.T) {
	e := newTestEnv(t)
	for _, args := range [][]string{{"sites"}, {"deploys", "x"}, {"rollback", "x"}, {"delete", "x", "--yes"}} {
		r := e.run(args...)
		expectCode(t, r, 1)
		expectContains(t, "stderr", r.stderr, "not logged in")
	}
}

func TestVersionHelpAndUsage(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("version")
	expectCode(t, r, 0)
	if r.stdout != brand.Name+" "+brand.Version+"\n" {
		t.Fatalf("version = %q", r.stdout)
	}
	expectCode(t, e.run("--version"), 0)
	expectCode(t, e.run("version", "x"), 2)

	r = e.run("help")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "deploy", "rollback", "DROPSITE_TOKEN")
	r = e.run("help", "rollback")
	expectCode(t, r, 0)
	expectContains(t, "stderr", r.stderr, "Usage: dropsite rollback")
	expectCode(t, e.run("help", "nope"), 2)

	r = e.run()
	expectCode(t, r, 2)
	expectContains(t, "stderr", r.stderr, "Commands:")
	r = e.run("frobnicate")
	expectCode(t, r, 2)
	expectContains(t, "stderr", r.stderr, `unknown command "frobnicate"`)
}

func TestParseArgsTerminator(t *testing.T) {
	a := &app{stderr: &strings.Builder{}}
	fs := a.newFlagSet("x", "", "")
	j := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, []string{"a", "--json", "--", "-b", "--json"})
	if err != nil || !*j || strings.Join(pos, ",") != "a,-b,--json" {
		t.Fatalf("pos=%v json=%v err=%v", pos, *j, err)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB"}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
