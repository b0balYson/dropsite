//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestPublicAdmin walks through access to the admin interface from the
// internet: closed by default (no certificate in ondemand mode, 404 in
// wildcard mode), opened in the settings with the password, used over
// HTTPS through Caddy, and closed again without reloading Caddy.
func TestPublicAdmin(t *testing.T) {
	s := up(t)
	host := "admin." + baseDomain
	base := "https://" + host + ":" + httpsPort

	t.Run("closed by default", func(t *testing.T) {
		resp, _, err := s.get(host, "/login")
		switch {
		case wildcardStand():
			// The wildcard certificate covers the name; control answers 404.
			if err != nil || resp.StatusCode != http.StatusNotFound {
				t.Fatalf("want 404, got %v %v", resp, err)
			}
		case err == nil:
			t.Fatalf("a certificate was issued for %s while public access is off (status %d)", host, resp.StatusCode)
		}
	})

	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	setPublic := func(on bool) {
		t.Helper()
		form := url.Values{"_csrf": {adminUI.csrf("/admin/settings")}, "_section": {"public-admin-h"}, "enabled": {"0"}}
		if on {
			form.Set("enabled", "1")
			form.Set("confirm", "1")
			form.Set("password", adminPass)
		}
		if status, body := adminUI.post("/admin/settings/public-admin", form); status != http.StatusSeeOther {
			t.Fatalf("set public admin %v: %d %.300s", on, status, body)
		}
	}
	setPublic(true)
	s.expectBody(host, "/login", http.StatusOK, "<altcha-widget")

	t.Run("sign in over HTTPS", func(t *testing.T) {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar, Transport: s.client.Transport, Timeout: 2 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Get(base + "/login") //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if hsts := resp.Header.Get("Strict-Transport-Security"); !strings.Contains(hsts, "max-age=") {
			t.Fatalf("HSTS %q", hsts)
		}
		m := csrfRe.FindSubmatch(b)
		if m == nil {
			t.Fatal("no CSRF token")
		}
		form := url.Values{"_csrf": {string(m[1])}, "email": {adminEmail}, "password": {adminPass}, "next": {"/"},
			"altcha": {solveAltcha(t, c, base+"/login/challenge")}}
		req, _ := http.NewRequest(http.MethodPost, base+"/login", strings.NewReader(form.Encode())) //nolint:noctx // test
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", base)
		resp, err = c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("sign in: %d", resp.StatusCode)
		}
		var session *http.Cookie
		for _, ck := range resp.Cookies() {
			if ck.Name == "__Host-ds_session" {
				session = ck
			}
		}
		if session == nil || !session.Secure || !session.HttpOnly {
			t.Fatalf("session cookie %+v", resp.Cookies())
		}
		resp, err = c.Get(base + "/") //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		b, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), adminEmail) {
			t.Fatalf("home: %d %.200s", resp.StatusCode, b)
		}
		// A cross-origin POST from a site on the base domain is refused.
		req, _ = http.NewRequest(http.MethodPost, base+"/sites", strings.NewReader("title=x")) //nolint:noctx // test
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://evil."+baseDomain+":"+httpsPort)
		resp, err = c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-origin post: %d", resp.StatusCode)
		}
	})

	// Closed again: the listener answers 404 at once; Caddy keeps running.
	setPublic(false)
	s.expectBody(host, "/login", http.StatusNotFound, "")
	s.expectBody(host, "/login/challenge", http.StatusNotFound, "")
	s.expectBody(host, "/static/altcha.js", http.StatusNotFound, "")
}
