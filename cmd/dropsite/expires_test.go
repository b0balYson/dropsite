package main

import (
	"testing"
	"time"
)

func TestParseExpires(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"7d":                        "2026-11-19T10:30:00Z",
		"36h":                       "2026-11-13T22:30:00Z",
		"2026-12-31":                "2027-01-01T00:00:00Z",
		"2026-12-01T12:00:00+03:00": "2026-12-01T09:00:00Z",
		"never":                     "null",
		" Never ":                   "null",
	} {
		e, err := parseExpires(in, now)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		got := "null"
		if e.At != nil {
			got = e.At.Format(time.RFC3339)
		}
		if got != want {
			t.Errorf("%q = %s, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"soon", "0d", "-1d", "7w", "d", "2026-13-01", "999999d"} {
		if _, err := parseExpires(bad, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDeployWithExpires(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, nil)
	r := e.run("deploy", dir, "--expires", "2026-12-31")
	expectCode(t, r, 0)
	if r.stdout != "https://site01.sites.test\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if got := e.srv.lastPatch["expires_at"]; got != "2027-01-01T00:00:00Z" {
		t.Fatalf("patched expires_at = %v", got)
	}
	expectContains(t, "stderr", r.stderr, "expires at 2027-01-01T00:00:00Z")

	r = e.run("deploy", dir, "--expires", "never")
	expectCode(t, r, 0)
	if v, ok := e.srv.lastPatch["expires_at"]; !ok || v != nil {
		t.Fatalf("never sent %v (present %v)", v, ok)
	}
}

func TestDeployBadExpiresFailsBeforeNetwork(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	before := e.srv.requestCount()
	r := e.run("deploy", siteDir(t, nil), "--expires", "soon")
	if r.code == 0 || e.srv.requestCount() != before {
		t.Fatalf("code %d, requests %d -> %d", r.code, before, e.srv.requestCount())
	}
	expectContains(t, "stderr", r.stderr, "--expires takes")
}

func TestDeployExpiresRejectedAfterPublish(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.patchErr = &apiErr{400, "expiry_invalid", "the expiry must be in the future", nil}
	r := e.run("deploy", siteDir(t, nil), "--expires", "2001-01-01")
	if r.code == 0 {
		t.Fatal("expected failure")
	}
	if r.stdout != "https://site01.sites.test\n" {
		t.Fatalf("the URL of the published site must still be printed: %q", r.stdout)
	}
	expectContains(t, "stderr", r.stderr, "published, but its expiry was not set")
}
