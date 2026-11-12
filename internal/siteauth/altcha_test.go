package siteauth

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

var metaNonceRe = regexp.MustCompile(`<meta name="csp-nonce" content="([^"]+)">`)

func TestLoginPageCarriesWidget(t *testing.T) {
	h := newHarness(t)
	page := h.serve(http.MethodGet, h.host, LoginPath, nil, withHeader("Accept-Language", "ru"))
	body := page.Body.String()
	for _, want := range []string{
		`<altcha-widget`, `challenge="` + ChallengePath + `"`, `auto="onload"`, `name="altcha"`,
		`src="` + widgetPath + `"`, `<noscript>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q:\n%s", want, body)
		}
	}
	m := metaNonceRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csp-nonce meta")
	}
	csp := page.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'", "script-src 'self'", "worker-src blob:", "connect-src 'self'",
		"style-src 'self' 'nonce-" + m[1] + "'", "form-action 'self'", "frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("csp %q lacks %q", csp, want)
		}
	}
	for _, banned := range []string{"unsafe-inline", "unsafe-eval", "data:", "*"} {
		if strings.Contains(csp, banned) {
			t.Fatalf("csp %q allows %q", csp, banned)
		}
	}
	// The style nonce is fresh per response.
	again := metaNonceRe.FindStringSubmatch(h.serve(http.MethodGet, h.host, LoginPath, nil).Body.String())
	if again == nil || again[1] == m[1] {
		t.Fatal("style nonce reused")
	}
	if got := page.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("referrer policy %q", got)
	}
	// Other responses keep the strict policy without scripts.
	if csp := h.serve(http.MethodGet, h.host, ChallengePath, nil).Header().Get("Content-Security-Policy"); strings.Contains(csp, "script-src") {
		t.Fatalf("challenge csp %q", csp)
	}
}

func TestLoginRequiresAltcha(t *testing.T) {
	h := newHarness(t)
	token, nonce := h.getForm(h.host)
	for name, payload := range map[string]string{"missing": "", "garbage": "!!", "tampered": h.solve(h.host) + "x"} {
		rec := h.serve(http.MethodPost, h.host, LoginPath,
			url.Values{"_csrf": {token}, "password": {sitePass}, "altcha": {payload}, "next": {"/docs/"}},
			withCookie(nonce), withHeader("Accept-Language", "ru"))
		if rec.Code != http.StatusBadRequest || accessCookie(rec) != nil {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "проверку браузера") || !strings.Contains(body, `name="next" value="/docs/"`) {
			t.Fatalf("%s: page\n%s", name, body)
		}
	}
	// A replayed solution is refused too.
	sol := h.solve(h.host)
	ok := h.serve(http.MethodPost, h.host, LoginPath, url.Values{"_csrf": {token}, "password": {sitePass}, "altcha": {sol}}, withCookie(nonce))
	if ok.Code != http.StatusSeeOther || accessCookie(ok) == nil {
		t.Fatalf("valid solution: %d", ok.Code)
	}
	rec := h.serve(http.MethodPost, h.host, LoginPath, url.Values{"_csrf": {token}, "password": {sitePass}, "altcha": {sol}}, withCookie(nonce))
	if rec.Code != http.StatusBadRequest || accessCookie(rec) != nil {
		t.Fatalf("replay: %d", rec.Code)
	}
}

// Cookies of both names carry the __Host- prefix, so browsers accept them
// only host-only, Secure and with Path=/.
func TestCookiesUseHostPrefix(t *testing.T) {
	h := newHarness(t)
	_, nonce := h.getForm(h.host)
	if !strings.HasPrefix(nonce.Name, "__Host-") || nonce.Path != "/" || nonce.Domain != "" || !nonce.Secure {
		t.Fatalf("nonce cookie %+v", nonce)
	}
	if !strings.HasPrefix(service.VisitorCookieName, "__Host-") {
		t.Fatalf("access cookie name %q", service.VisitorCookieName)
	}
}

// A neighbouring site under the base domain could plant an invalid cookie
// of the same name (Domain=base, longer Path), which the browser sends
// first. A valid cookie next to it still grants access.
func TestTossedAccessCookieDoesNotShadowValid(t *testing.T) {
	h := newHarness(t)
	token, nonce := h.getForm(h.host)
	rec := h.serve(http.MethodPost, h.host, LoginPath, h.form(token, sitePass, "/"), withCookie(nonce))
	good := accessCookie(rec)
	if good == nil {
		t.Fatalf("login failed: %d", rec.Code)
	}
	tossed := &http.Cookie{Name: good.Name, Value: "x"}
	if r := h.authCheck(h.host, "/", tossed, good); r.Code != http.StatusOK {
		t.Fatalf("tossed + valid: %d", r.Code)
	}
	if r := h.authCheck(h.host, "/", tossed); r.Code != http.StatusFound {
		t.Fatalf("tossed alone: %d", r.Code)
	}
}

// The same for the CSRF nonce: a planted nonce (malformed or well-formed)
// sent before the real one does not break the login.
func TestTossedNonceDoesNotBlockLogin(t *testing.T) {
	h := newHarness(t)
	for _, planted := range []string{"bad", strings.Repeat("A", 24)} {
		tossed := &http.Cookie{Name: nonceCookie, Value: planted}
		page := h.serve(http.MethodGet, h.host, LoginPath, nil, withCookie(tossed))
		m := csrfRe.FindStringSubmatch(page.Body.String())
		var fresh *http.Cookie
		for _, c := range page.Result().Cookies() {
			if c.Name == nonceCookie {
				fresh = c
			}
		}
		if m == nil || fresh == nil {
			t.Fatalf("%q: page %d", planted, page.Code)
		}
		cookies := []func(*http.Request){withCookie(tossed)}
		if fresh.Value != planted {
			cookies = append(cookies, withCookie(fresh))
		}
		rec := h.serve(http.MethodPost, h.host, LoginPath, h.form(m[1], sitePass, "/"),
			append(cookies, withHeader("Origin", "https://"+h.host), withHeader("Sec-Fetch-Site", "same-origin"))...)
		if rec.Code != http.StatusSeeOther || accessCookie(rec) == nil {
			t.Fatalf("%q: login with a planted nonce first: %d", planted, rec.Code)
		}
	}
}
