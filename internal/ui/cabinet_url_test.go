package ui

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var anyInviteRe = regexp.MustCompile(`<code>(https?://[^<]+/invite/[A-Za-z0-9_-]+)</code>`)

// The invitation link shown to the administrator uses the public address
// while public access is on (made when it is shown), and the token opens
// on both listeners whatever the switch says later.
func TestInviteLinkFollowsPublicAccess(t *testing.T) {
	p := newPublicHarness(t)
	h := p.loop
	h.loginAdmin()

	invite := func(email string) string {
		t.Helper()
		pg := h.submit("/admin/users", "/admin/users", url.Values{"email": {email}, "role": {"user"}})
		wantStatus(t, pg, http.StatusOK)
		m := anyInviteRe.FindStringSubmatch(pg.body)
		if m == nil {
			t.Fatalf("no invite link:\n%.400s", pg.body)
		}
		return m[1]
	}
	path := func(link string) string { return link[strings.Index(link, "/invite/"):] }

	off := invite("off@pp.test")
	if !strings.HasPrefix(off, "http://127.0.0.1:8082/invite/") {
		t.Fatalf("link while off: %q", off)
	}
	setPublic(t, h, true)
	on := invite("on@pp.test")
	if !strings.HasPrefix(on, "https://"+publicHost+"/invite/") {
		t.Fatalf("link while on: %q", on)
	}
	for _, link := range []string{off, on} {
		wantStatus(t, h.get(path(link)), http.StatusOK)
		wantStatus(t, p.get(path(link)), http.StatusOK)
	}
	setPublic(t, h, false)
	if again := invite("later@pp.test"); !strings.HasPrefix(again, "http://127.0.0.1:8082/invite/") {
		t.Fatalf("link after turning off: %q", again)
	}
	wantStatus(t, h.get(path(on)), http.StatusOK)
}

// The Connect AI page tells where the interface is while public access is on.
func TestConnectPageShowsPublicURL(t *testing.T) {
	p := newPublicHarness(t)
	h := p.loop
	h.loginAdmin()
	wantNotContains(t, h.get("/connect").body, "https://"+publicHost)
	setPublic(t, h, true)
	wantContains(t, h.get("/connect").body, "https://"+publicHost)
}
