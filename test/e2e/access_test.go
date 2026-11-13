//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

var loginCSRFRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// TestSiteAccess walks through the stage-2 access controls: noindex,
// password protection, independence of open sites from control, expiry.
func TestSiteAccess(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}
	ann := createUser(t, s, admin, "ann@dropsite.localhost")

	newSite := func(title string) (string, string) {
		t.Helper()
		st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": title})
		must(t, st, http.StatusCreated, body, "create "+title)
		id := str(body, "id")
		st, body = ann.deploy(id, makeZip(t, site(map[string]string{
			"index.html": "<h1>" + title + "</h1>", "404.html": "custom 404 of " + title, "200.html": "spa of " + title,
			"app.css": "body{color:#123}",
		})...))
		must(t, st, http.StatusCreated, body, "deploy "+title)
		host := id + "." + baseDomain
		s.expectBody(host, "/", 200, title)
		return id, host
	}
	openID, openHost := newSite("Open")
	_, plainHost := newSite("Plain")
	lockedID, lockedHost := newSite("Locked")

	t.Run("noindex header", func(t *testing.T) {
		st, body := ann.json(http.MethodPatch, "/sites/"+openID, map[string]any{"noindex": true})
		must(t, st, http.StatusOK, body, "set noindex")
		for _, path := range []string{"/", "/missing", "/.env"} {
			s.eventually("noindex on "+path, func() error {
				resp, _, err := s.get(openHost, path) //nolint:bodyclose // s.get closes it
				if err != nil {
					return err
				}
				if got := resp.Header.Values("X-Robots-Tag"); len(got) != 1 || got[0] != "noindex, nofollow" {
					return fmt.Errorf("X-Robots-Tag %q", got)
				}
				return nil
			})
		}
		resp, _, err := s.get(plainHost, "/") //nolint:bodyclose // s.get closes it
		if err != nil || resp.Header.Get("X-Robots-Tag") != "" {
			t.Fatalf("plain site: %v %v", resp.Header, err)
		}
	})

	t.Run("protected site requires the password", func(t *testing.T) {
		st, body := ann.json(http.MethodPatch, "/sites/"+lockedID, map[string]any{"password": "visitor-pass-1"})
		must(t, st, http.StatusOK, body, "protect")
		for _, path := range []string{"/", "/404.html", "/200.html", "/missing/route"} {
			resp, b, err := s.get(lockedHost, path) //nolint:bodyclose // s.get closes it
			if err != nil || resp.StatusCode != http.StatusFound || strings.Contains(b, "Locked") || strings.Contains(b, "spa of") {
				t.Fatalf("%s without access: %v %v %.100q", path, resp, err, b)
			}
			if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/_dropsite/login?next=") {
				t.Fatalf("%s: location %q", path, loc)
			}
		}
		cookie := visitorLogin(t, s, lockedHost, "visitor-pass-1")
		resp, b, err := s.get(lockedHost, "/missing/route", "Cookie", cookie) //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(b, "spa of Locked") {
			t.Fatalf("with access: %v %v %.100q", resp, err, b)
		}
		// No shared cache may store anything of a protected site; open
		// sites keep their caching rules.
		cacheControl := func(host, path string, hdr ...string) string {
			t.Helper()
			resp, _, err := s.get(host, path, hdr...) //nolint:bodyclose // s.get closes it
			if err != nil {
				t.Fatalf("%s%s: %v", host, path, err)
			}
			return resp.Header.Get("Cache-Control")
		}
		for _, path := range []string{"/app.css", "/", "/_dropsite/data/nope", "/missing.css"} {
			if got := cacheControl(lockedHost, path, "Cookie", cookie); got != "private, no-store" {
				t.Errorf("protected %s: Cache-Control %q", path, got)
			}
		}
		if got := cacheControl(plainHost, "/app.css"); got != "public, max-age=3600" {
			t.Errorf("open asset: Cache-Control %q", got)
		}
		// Changing the password signs everybody out.
		st, body = ann.json(http.MethodPatch, "/sites/"+lockedID, map[string]any{"password": "visitor-pass-2"})
		must(t, st, http.StatusOK, body, "change password")
		resp, _, err = s.get(lockedHost, "/", "Cookie", cookie) //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusFound {
			t.Fatalf("old cookie after change: %v %v", resp, err)
		}
	})

	t.Run("open sites do not depend on control", func(t *testing.T) {
		run(t, s.ctx, "stop", "control")
		defer func() {
			run(t, s.ctx, "start", "control")
			s.waitHTTP(uiURL+"/login", 60*time.Second)
		}()
		s.expectBody(openHost, "/", 200, "Open")
		s.expectBody(plainHost, "/", 200, "Plain")
		resp, b, err := s.get(lockedHost, "/") //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusServiceUnavailable || strings.Contains(b, "Locked") {
			t.Fatalf("protected site with control down: %v %v %.100q", resp, err, b)
		}
		// control logs every auth check; none was made for the open sites.
		logs := run(t, s.ctx, "logs", "--no-color", "control")
		for _, h := range []string{openHost, plainHost} {
			if strings.Contains(logs, `"msg":"site auth check","host":"`+h+`"`) {
				t.Fatalf("control was asked about open site %s", h)
			}
		}
	})

	t.Run("expired site stops opening", func(t *testing.T) {
		at := time.Now().UTC().Add(5 * time.Second).Format(time.RFC3339)
		st, body := ann.json(http.MethodPatch, "/sites/"+openID, map[string]any{"expires_at": at})
		must(t, st, http.StatusOK, body, "set expiry")
		deadline := time.Now().Add(2 * time.Minute)
		for {
			resp, _, err := s.get(openHost, "/") //nolint:bodyclose // s.get closes it
			if err == nil && resp.StatusCode == http.StatusNotFound {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("expired site still served: %v %v", resp, err)
			}
			time.Sleep(2 * time.Second)
		}
		st, _ = ann.json(http.MethodGet, "/sites/"+openID, nil)
		if st != http.StatusNotFound {
			t.Fatalf("expired site via API: %d", st)
		}
	})
}

// visitorLogin signs in on a protected site's login page and returns the
// Cookie header value for following requests.
func visitorLogin(t *testing.T, s *stack, host, password string) string {
	t.Helper()
	resp, page, err := s.get(host, "/_dropsite/login?next=/") //nolint:bodyclose // s.get closes it
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login page: %v %v", resp, err)
	}
	m := loginCSRFRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no csrf token:\n%.300s", page)
	}
	var nonce string
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-dropsite_login_nonce" {
			nonce = c.Name + "=" + c.Value
		}
	}
	post := func(form url.Values) *http.Response {
		req, _ := http.NewRequestWithContext(s.ctx, http.MethodPost, "https://"+host+":"+httpsPort+"/_dropsite/login",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://"+host+":"+httpsPort)
		req.Header.Set("Cookie", nonce)
		res, err := s.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	res := post(url.Values{"_csrf": {m[1]}, "password": {password}, "next": {"/"}, "altcha": {loginAltcha(t, s, host)}}) //nolint:bodyclose // post closes it
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", res.StatusCode)
	}
	// Without an Altcha solution the password is not even checked.
	if res := post(url.Values{"_csrf": {m[1]}, "password": {password}}); res.StatusCode != http.StatusBadRequest { //nolint:bodyclose // post closes it
		t.Fatalf("login without altcha: %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == "__Host-dropsite_access" {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatal("no access cookie")
	return ""
}

// TestCaddyfileValidates runs "caddy validate" on deploy/Caddyfile in both
// TLS modes with the dropsite-caddy image of the stack (built when missing).
func TestCaddyfileValidates(t *testing.T) {
	cf := filepath.Join(repoRoot(), "deploy", "Caddyfile")
	if out, err := compose(t.Context(), "build", "caddy").CombinedOutput(); err != nil {
		t.Fatalf("build caddy: %v\n%s", err, out)
	}
	for _, mode := range []string{"ondemand", "wildcard"} {
		out, err := exec.CommandContext(t.Context(), "docker", "run", "--rm",
			"-e", "TLS_MODE="+mode, "-e", "TLS_DNS_PROVIDER=internal",
			"-e", "BASE_DOMAIN=example.com", "-e", "ACME_EMAIL=a@example.com", "-e", "API_HOST=api.example.com",
			"-e", "ADMIN_HOST=admin.example.com",
			"-v", cf+":/etc/caddy/Caddyfile:ro", "ghcr.io/b0balyson/dropsite-caddy:dev",
			"caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "Valid configuration") {
			t.Fatalf("%s: %v\n%s", mode, err, out)
		}
	}
}

// loginAltcha fetches the login challenge through Caddy without any cookie
// (the path is proxied before forward_auth) and solves it like the widget.
func loginAltcha(t *testing.T, s *stack, host string) string {
	t.Helper()
	resp, body, err := s.get(host, "/_dropsite/login/challenge") //nolint:bodyclose // s.get closes it
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login challenge: %v %v %.200s", statusOf(resp), err, body)
	}
	var ch altcha.Challenge
	if err := json.Unmarshal([]byte(body), &ch); err != nil {
		t.Fatalf("login challenge: %v", err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: 2 * time.Minute})
	if err != nil || sol == nil {
		t.Fatalf("solve login challenge: %v", err)
	}
	b, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	return base64.StdEncoding.EncodeToString(b)
}
