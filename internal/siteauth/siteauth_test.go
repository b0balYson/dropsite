package siteauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

const sitePass = "visitor-pass-1"

type harness struct {
	t     *testing.T
	env   *testenv.Env
	check http.Handler
	login http.Handler
	host  string // slug host of a protected site
	open  string // default host of an open site
	ip    string // visitor address seen by the login handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	e := testenv.New(t)
	ctx := context.Background()
	owner := e.User("owner@pp.test")
	v, err := e.Svc.CreateSite(ctx, owner, `Docs <b>"quoted"</b>`, "docs")
	if err != nil {
		t.Fatal(err)
	}
	pass := sitePass
	if _, err := e.Svc.UpdateSite(ctx, owner, v.Site.ID, service.SiteUpdate{Password: &pass}); err != nil {
		t.Fatal(err)
	}
	open, err := e.Svc.CreateSite(ctx, owner, "Open", "")
	if err != nil {
		t.Fatal(err)
	}
	// The lowest base difficulty keeps solving cheap in tests.
	m := forms.MinAltchaMaxNumber
	if _, err := e.Svc.UpdateFormSettings(ctx, e.Admin, service.FormSettingsUpdate{AltchaMaxNumber: &m}); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, env: e, check: AuthCheck(e.Svc, nil),
		host: "docs." + testenv.BaseDomain, open: open.Site.ID + "." + testenv.BaseDomain, ip: "203.0.113.7",
	}
	h.login = Login(e.Svc, nil, func(*http.Request) string { return h.ip })
	return h
}

// solve fetches the login challenge of host without any cookie and solves
// it like the widget does.
func (h *harness) solve(host string) string {
	h.t.Helper()
	rec := h.serve(http.MethodGet, host, ChallengePath, nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		h.t.Fatalf("challenge: %d %s", rec.Code, rec.Body)
	}
	var ch altcha.Challenge
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		h.t.Fatal(err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: time.Minute})
	if err != nil || sol == nil {
		h.t.Fatalf("solve: %v", err)
	}
	b, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	return base64.StdEncoding.EncodeToString(b)
}

// form is a login submission with a fresh Altcha solution.
func (h *harness) form(token, password, next string) url.Values {
	h.t.Helper()
	v := url.Values{"_csrf": {token}, "password": {password}, "altcha": {h.solve(h.host)}}
	if next != "" {
		v.Set("next", next)
	}
	return v
}

func (h *harness) authCheck(host, uri string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://control:8081/internal/auth-check", nil)
	req.Host = host
	req.Header.Set("X-Forwarded-Uri", uri)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.check.ServeHTTP(rec, req)
	return rec
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// getForm loads the login page and returns the CSRF token and nonce cookie.
func (h *harness) getForm(host string) (string, *http.Cookie) {
	h.t.Helper()
	rec := h.serve(http.MethodGet, host, LoginPath+"?next=/docs/", nil)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login page: %d %s", rec.Code, rec.Body)
	}
	m := csrfRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		h.t.Fatal("no csrf token")
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == nonceCookie {
			return m[1], c
		}
	}
	h.t.Fatal("no nonce cookie")
	return "", nil
}

func (h *harness) serve(method, host, target string, form url.Values, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, "https://"+host+target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, "https://"+host+target, nil)
	}
	for _, m := range mods {
		m(req)
	}
	rec := httptest.NewRecorder()
	h.login.ServeHTTP(rec, req)
	return rec
}

func withCookie(c *http.Cookie) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(c) }
}

func withHeader(k, v string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func accessCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == service.VisitorCookieName {
			return c
		}
	}
	return nil
}

func TestAuthCheckDecisions(t *testing.T) {
	h := newHarness(t)
	rec := h.authCheck(h.host+":443", "/docs/page?x=1")
	if rec.Code != http.StatusFound {
		t.Fatalf("no cookie: %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Path != LoginPath || loc.Query().Get("next") != "/docs/page?x=1" || loc.Host != "" {
		t.Fatalf("location %q", rec.Header().Get("Location"))
	}
	if rec := h.authCheck(h.open, "/"); rec.Code != http.StatusOK {
		t.Fatalf("open site: %d", rec.Code)
	}
	for _, host := range []string{"unknown.pp.test", "..", "", "DOCS.pp.test"} {
		if rec := h.authCheck(host, "/"); rec.Code != http.StatusNotFound {
			t.Fatalf("%q: %d", host, rec.Code)
		}
	}
	// An unsafe original URI is never echoed into the redirect.
	rec = h.authCheck(h.host, "//evil.example/x")
	if got := rec.Header().Get("Location"); got != LoginPath+"?next=%2F" {
		t.Fatalf("location %q", got)
	}
	if rec := h.authCheck(h.host, "/", &http.Cookie{Name: service.VisitorCookieName, Value: "forged.1.2.3"}); rec.Code != http.StatusFound {
		t.Fatalf("forged cookie: %d", rec.Code)
	}
}

func TestLoginFlow(t *testing.T) {
	h := newHarness(t)
	page := h.serve(http.MethodGet, h.host, LoginPath+"?next=/docs/", nil, withHeader("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8"))
	body := page.Body.String()
	for _, want := range []string{`lang="ru"`, "Открыть сайт", `Docs &lt;b&gt;&#34;quoted&#34;&lt;/b&gt;`, `name="next" value="/docs/"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q:\n%s", want, body)
		}
	}
	// Apart from the reserved paths, the page never names the platform.
	if strings.Contains(strings.ToLower(strings.ReplaceAll(body, Prefix, "")), "dropsite") {
		t.Fatal("login page mentions the platform")
	}
	for k, want := range map[string]string{
		"X-Robots-Tag": "noindex, nofollow", "Cache-Control": "no-store", "X-Frame-Options": "DENY",
	} {
		if got := page.Header().Get(k); got != want {
			t.Fatalf("%s = %q", k, got)
		}
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("csp %q", csp)
	}

	token, nonce := h.getForm(h.host)
	if !nonce.HttpOnly || !nonce.Secure || nonce.SameSite != http.SameSiteStrictMode || nonce.Path != "/" {
		t.Fatalf("nonce cookie %+v", nonce)
	}
	// Wrong password: 401, form again, no access cookie.
	rec := h.serve(http.MethodPost, h.host, LoginPath, h.form(token, "wrong-pass-123", "/docs/"), withCookie(nonce))
	if rec.Code != http.StatusUnauthorized || accessCookie(rec) != nil || !strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	// Right password with a hostile next: cookie set, redirect stays local.
	rec = h.serve(http.MethodPost, h.host, LoginPath,
		h.form(token, sitePass, "https://evil.example/"),
		withCookie(nonce), withHeader("Origin", "https://"+h.host), withHeader("Sec-Fetch-Site", "same-origin"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	c := accessCookie(rec)
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("access cookie %+v", c)
	}
	if c.MaxAge < int((service.VisitorCookieTTL-time.Minute)/time.Second) {
		t.Fatalf("max age %d", c.MaxAge)
	}
	if rec := h.authCheck(h.host, "/docs/", c); rec.Code != http.StatusOK {
		t.Fatalf("auth with cookie: %d", rec.Code)
	}
	// Next is kept when it is a local path.
	rec = h.serve(http.MethodPost, h.host, LoginPath,
		h.form(token, sitePass, "/docs/?q=1"), withCookie(nonce))
	if rec.Header().Get("Location") != "/docs/?q=1" {
		t.Fatalf("next %q", rec.Header().Get("Location"))
	}
	// Firefox sends "Origin: null" for a same-origin POST under a strict
	// referrer policy; Fetch Metadata confirms it is same-origin.
	rec = h.serve(http.MethodPost, h.host, LoginPath, h.form(token, sitePass, ""),
		withCookie(nonce), withHeader("Origin", "null"), withHeader("Sec-Fetch-Site", "same-origin"))
	if rec.Code != http.StatusSeeOther || accessCookie(rec) == nil {
		t.Fatalf("origin null, same-origin: %d", rec.Code)
	}
}

func TestLoginCSRF(t *testing.T) {
	h := newHarness(t)
	token, nonce := h.getForm(h.host)
	good := url.Values{"_csrf": {token}, "password": {sitePass}}
	cases := map[string]*httptest.ResponseRecorder{
		"no token":       h.serve(http.MethodPost, h.host, LoginPath, url.Values{"password": {sitePass}}, withCookie(nonce)),
		"no nonce":       h.serve(http.MethodPost, h.host, LoginPath, good),
		"cross site":     h.serve(http.MethodPost, h.host, LoginPath, good, withCookie(nonce), withHeader("Sec-Fetch-Site", "cross-site")),
		"foreign origin": h.serve(http.MethodPost, h.host, LoginPath, good, withCookie(nonce), withHeader("Origin", "https://evil.example")),
		"null origin":    h.serve(http.MethodPost, h.host, LoginPath, good, withCookie(nonce), withHeader("Origin", "null")),
		"other nonce": h.serve(http.MethodPost, h.host, LoginPath, good,
			withCookie(&http.Cookie{Name: nonceCookie, Value: strings.Repeat("A", 24)})),
	}
	for name, rec := range cases {
		if rec.Code != http.StatusForbidden || accessCookie(rec) != nil {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	// A token for one host does not work on another address.
	open := h.serve(http.MethodPost, h.open, LoginPath, good, withCookie(nonce))
	if open.Code != http.StatusNotFound && open.Code != http.StatusForbidden {
		t.Fatalf("other host: %d", open.Code)
	}
}

// The per-source limit itself is tested in the service; here only its page.
func TestLoginRateLimitedPage(t *testing.T) {
	h := newHarness(t)
	l := &login{svc: h.env.Svc, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequest(http.MethodPost, "https://"+h.host+LoginPath, nil)
	rec := httptest.NewRecorder()
	l.loginFailed(rec, req, "/", catalog["en"], domain.ErrRateLimited.WithRetryAfter(15*time.Minute))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "900" || accessCookie(rec) != nil {
		t.Fatalf("rate limit: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "15 min") {
		t.Fatalf("message:\n%s", rec.Body)
	}
}

func TestLoginOtherPaths(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		host, path string
		want       int
	}{
		{h.open, LoginPath, http.StatusNotFound},
		{"api.pp.test", LoginPath, http.StatusNotFound},
		{h.host, Prefix + "other", http.StatusNotFound},
		{h.host, cssPath, http.StatusOK},
		{h.host, ChallengePath, http.StatusOK},
		{h.open, ChallengePath, http.StatusNotFound},
		{"unknown.pp.test", ChallengePath, http.StatusNotFound},
	} {
		rec := h.serve(http.MethodGet, tc.host, tc.path, nil)
		if rec.Code != tc.want {
			t.Fatalf("%s%s: %d", tc.host, tc.path, rec.Code)
		}
		if rec.Header().Get("X-Robots-Tag") == "" {
			t.Fatalf("%s%s: no X-Robots-Tag", tc.host, tc.path)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "/",
		"/":                                "/",
		"/a/b?c=1#d":                       "/a/b?c=1#d",
		"//evil.example":                   "/",
		"/\\evil.example":                  "/",
		"https://evil.example":             "/",
		"javascript:alert(1)":              "/",
		"/x\r\nSet-Cookie: a":              "/",
		"/_dropsite/login":                 "/",
		"/_dropsite":                       "/",
		"/" + strings.Repeat("a", maxNext): "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickLang(t *testing.T) {
	for in, want := range map[string]string{
		"": "en", "ru": "ru", "ru-RU,en;q=0.5": "ru", "de-DE,en-US;q=0.8,ru;q=0.5": "en", "fr": "en",
	} {
		if got := pickLang(in); got != want {
			t.Errorf("pickLang(%q) = %q", in, got)
		}
	}
}
