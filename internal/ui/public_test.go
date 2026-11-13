package ui

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

const publicHost = "admin." + testenv.BaseDomain

// publicHarness is a browser on the public listener, as Caddy forwards it:
// Host admin.<base domain> and one X-Forwarded-For hop.
type publicHarness struct {
	*harness
	host string
	xff  string
}

func newPublicHarness(t *testing.T) *publicHarness {
	t.Helper()
	h := newHarness(t)
	pub := httptest.NewServer(NewPublic(h.env.Svc, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(pub.Close)
	p := &publicHarness{harness: newHarnessSharing(t, h), host: publicHost, xff: "203.0.113.10"}
	p.srv = pub
	p.c.Transport = p
	return p
}

// RoundTrip sets the Host and X-Forwarded-For of every request.
func (p *publicHarness) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = p.host
	if p.xff != "" {
		r.Header.Set("X-Forwarded-For", p.xff)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func setPublic(t *testing.T, h *harness, on bool) {
	t.Helper()
	if err := h.env.Svc.SetPublicAdmin(context.Background(), h.env.Admin, on, true, testenv.AdminPassword); err != nil {
		t.Fatal(err)
	}
}

// While public access is off every route answers a bare 404 without
// cookies: pages, static files, the Altcha challenge, the OAuth paths and
// unsafe methods alike.
func TestPublicListenerIsClosedWhileOff(t *testing.T) {
	p := newPublicHarness(t)
	paths := []string{"/", "/login", "/login/challenge", "/static/app.css", "/static/altcha.js",
		"/favicon.ico", "/invite/x", "/oauth/authorize", "/admin/settings", "/connect", "/nope"}
	check := func(when string) {
		t.Helper()
		for _, path := range paths {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				req, _ := http.NewRequestWithContext(context.Background(), method, p.srv.URL+path, strings.NewReader("a=b"))
				pg := p.do(req)
				if pg.status != http.StatusNotFound || len(pg.resp.Cookies()) != 0 || strings.Contains(pg.body, "<html") {
					t.Fatalf("%s %s %s: %d cookies=%v %.100s", when, method, path, pg.status, pg.resp.Cookies(), pg.body)
				}
			}
		}
	}
	check("never enabled")
	setPublic(t, p.harness, true)
	wantStatus(t, p.get("/login"), http.StatusOK)
	setPublic(t, p.harness, false)
	check("turned off")
}

func TestPublicListenerChecksHost(t *testing.T) {
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	for host, want := range map[string]int{
		publicHost:                           http.StatusOK,
		strings.ToUpper(publicHost):          http.StatusOK,
		publicHost + ":443":                  http.StatusOK,
		publicHost + ".":                     http.StatusOK,
		publicHost + ":8443":                 http.StatusNotFound,
		"control:8084":                       http.StatusNotFound,
		"127.0.0.1:8084":                     http.StatusNotFound,
		testenv.BaseDomain:                   http.StatusNotFound,
		"docs." + testenv.BaseDomain:         http.StatusNotFound,
		publicHost + ".evil.example":         http.StatusNotFound,
		"evil.example":                       http.StatusNotFound,
		"x" + publicHost:                     http.StatusNotFound,
		"api." + testenv.BaseDomain + ":443": http.StatusNotFound,
	} {
		p.host = host
		if got := p.get("/login"); got.status != want {
			t.Errorf("Host %q: %d, want %d", host, got.status, want)
		}
	}
}

func TestPublicListenerCookiesAndHeaders(t *testing.T) {
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	page := p.get("/login")
	wantStatus(t, page, http.StatusOK)
	if hsts := page.resp.Header.Get("Strict-Transport-Security"); !strings.Contains(hsts, "max-age=") {
		t.Fatalf("HSTS %q", hsts)
	}
	login := p.login(testenv.AdminEmail, testenv.AdminPassword)
	wantStatus(t, login, http.StatusSeeOther)
	cookies := append(page.resp.Cookies(), login.resp.Cookies()...)
	var names []string
	for _, c := range cookies {
		names = append(names, c.Name)
		if !strings.HasPrefix(c.Name, "__Host-") || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
			t.Errorf("cookie %+v", c)
		}
	}
	if !strings.Contains(strings.Join(names, " "), "__Host-"+sessionCookieName) {
		t.Fatalf("no session cookie in %v", names)
	}
}

// A cookie without the prefix (planted by a site on a sibling subdomain)
// is never read on the public listener.
func TestPublicListenerIgnoresUnprefixedCookies(t *testing.T) {
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	res, err := p.env.Svc.Login(context.Background(), testenv.AdminEmail, testenv.AdminPassword, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.srv.URL+"/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: res.Secret})
	pg := p.do(req)
	if pg.status != http.StatusSeeOther || !strings.HasPrefix(pg.location(), "/login") {
		t.Fatalf("unprefixed session accepted: %d %s", pg.status, pg.location())
	}
	// A planted double-submit token does not help a cross-site login either.
	planted := strings.Repeat("ab", anonTokenBytes)
	form := url.Values{"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword}, csrfField: {planted}}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, p.srv.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: anonCookieName, Value: planted})
	wantStatus(t, p.do(req), http.StatusForbidden)
}

// Unsafe requests must come from the public origin exactly.
func TestPublicListenerOrigin(t *testing.T) {
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	p.loginAdmin()
	origin := "https://" + publicHost
	for name, hdr := range map[string][]string{
		"sibling site":   {"Origin", "https://docs." + testenv.BaseDomain},
		"plain http":     {"Origin", "http://" + publicHost},
		"other port":     {"Origin", origin + ":8443"},
		"null":           {"Origin", "null"},
		"same-site":      {"Origin", origin, "Sec-Fetch-Site", "same-site"},
		"cross-site":     {"Sec-Fetch-Site", "cross-site"},
		"loopback":       {"Origin", "http://127.0.0.1:8082"},
		"base domain":    {"Origin", "https://" + testenv.BaseDomain},
		"suffix trick":   {"Origin", origin + ".evil.example"},
		"userinfo trick": {"Origin", "https://evil@" + publicHost},
	} {
		pg := p.submit("/", "/sites", url.Values{"title": {"X"}}, hdr...)
		if pg.status != http.StatusForbidden {
			t.Errorf("%s: %d", name, pg.status)
		}
	}
	ok := p.submit("/", "/sites", url.Values{"title": {"Y"}}, "Origin", origin, "Sec-Fetch-Site", "same-origin")
	wantStatus(t, ok, http.StatusSeeOther)
}

// The login limit counts the client address Caddy forwards, so one client
// is limited without locking out the others.
func TestPublicListenerLimitsPerClient(t *testing.T) {
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	var pg page
	for range service.FailedLoginsPerIP + 1 {
		pg = p.login("x@pp.test", "wrong-password-1")
	}
	wantStatus(t, pg, http.StatusTooManyRequests)
	p.xff = "203.0.113.11"
	wantStatus(t, p.login(testenv.AdminEmail, testenv.AdminPassword), http.StatusSeeOther)
	// The audit log records the forwarded address.
	rows, _, err := p.env.Svc.ListAudit(context.Background(), p.env.Admin, service.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].Action != "auth.login" || rows[0].IP != "203.0.113.11" {
		t.Fatalf("audit %+v", rows[0])
	}
}

func TestForwardedIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("127.0.0.0/8")}
	cases := []struct {
		peer string
		xff  []string
		want string
	}{
		{"172.18.0.3:4000", []string{"203.0.113.5"}, "203.0.113.5"},
		// Only the rightmost hop is believed: Caddy replaces the header.
		{"172.18.0.3:4000", []string{"6.6.6.6, 203.0.113.5"}, "203.0.113.5"},
		{"172.18.0.3:4000", []string{"6.6.6.6", "203.0.113.5"}, "203.0.113.5"},
		{"172.18.0.3:4000", []string{"::ffff:203.0.113.5"}, "203.0.113.5"},
		{"172.18.0.3:4000", nil, "172.18.0.3"},
		{"172.18.0.3:4000", []string{"garbage"}, "172.18.0.3"},
		// From outside the trusted networks the header is ignored.
		{"198.51.100.9:4000", []string{"203.0.113.5"}, "198.51.100.9"},
		{"[2001:db8::1]:4000", []string{"203.0.113.5"}, "2001:db8::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.peer
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := forwardedIP(r, trusted); got != c.want {
			t.Errorf("peer %s xff %v: %q, want %q", c.peer, c.xff, got, c.want)
		}
	}
}

var cspNonceRe = regexp.MustCompile(`<meta name="csp-nonce" content="([^"]+)">`)

// The sign-in and invitation pages carry the Altcha widget on both
// listeners, solve it on submit and say that JavaScript is needed.
func TestLoginPageCarriesAltcha(t *testing.T) {
	h := newHarness(t)
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	link, err := h.env.Svc.CreateUser(context.Background(), h.env.Admin, "new@pp.test", "user")
	if err != nil {
		t.Fatal(err)
	}
	for name, get := range map[string]func() page{
		"loopback login":  func() page { return h.get("/login") },
		"public login":    func() page { return p.get("/login") },
		"loopback invite": func() page { return h.get("/invite/" + testenv.InviteSecret(link.URL)) },
	} {
		pg := get()
		wantStatus(t, pg, http.StatusOK)
		wantContains(t, pg.body, `<altcha-widget`, `challenge="/login/challenge"`, `auto="onsubmit"`, `name="altcha"`,
			`/static/altcha.js?v=`, "<noscript>", "нужен JavaScript")
		m := cspNonceRe.FindStringSubmatch(pg.body)
		if m == nil {
			t.Fatalf("%s: no csp-nonce", name)
		}
		if got := pg.resp.Header.Get("Content-Security-Policy"); got != altchaCSP(m[1]) {
			t.Fatalf("%s: csp %q", name, got)
		}
	}
	for _, c := range []*harness{h, p.harness} {
		js := c.get("/static/altcha.js")
		wantStatus(t, js, http.StatusOK)
		if ct := js.resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Fatalf("widget content type %q", ct)
		}
	}
}

func TestLoginRequiresAltcha(t *testing.T) {
	h := newHarness(t)
	for name, payload := range map[string]string{"missing": "", "garbage": "!!", "tampered": h.solve() + "x"} {
		pg := h.submit("/login", "/login", url.Values{"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword},
			"altcha": {payload}, "next": {"/connect"}})
		wantStatus(t, pg, http.StatusBadRequest)
		wantContains(t, pg.body, "проверку браузера", `name="next" value="/connect"`, testenv.AdminEmail)
		if strings.Contains(fmt.Sprint(pg.resp.Cookies()), sessionCookieName+"=") {
			t.Fatalf("%s: signed in", name)
		}
	}
	// A solution for the public host is not accepted on the loopback one.
	p := newPublicHarness(t)
	setPublic(t, p.harness, true)
	foreign := p.solve()
	pg := h.submit("/login", "/login", url.Values{"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword}, "altcha": {foreign}})
	wantStatus(t, pg, http.StatusBadRequest)
	// A replayed solution is refused.
	sol := h.solve()
	wantStatus(t, h.submit("/login", "/login", url.Values{"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword}, "altcha": {sol}}), http.StatusSeeOther)
	other := newHarnessSharing(t, h)
	wantStatus(t, other.submit("/login", "/login", url.Values{"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword}, "altcha": {sol}}), http.StatusBadRequest)
}

func TestInviteRequiresAltcha(t *testing.T) {
	h := newHarness(t)
	link, err := h.env.Svc.CreateUser(context.Background(), h.env.Admin, "new@pp.test", "user")
	if err != nil {
		t.Fatal(err)
	}
	path := "/invite/" + testenv.InviteSecret(link.URL)
	pg := h.submit(path, path, url.Values{"password": {"long-password-1"}, "password2": {"long-password-1"}})
	wantStatus(t, pg, http.StatusBadRequest)
	wantContains(t, pg.body, "проверку браузера")
	if _, err := h.env.Svc.InviteUser(context.Background(), testenv.InviteSecret(link.URL)); err != nil {
		t.Fatal("the invitation was spent without Altcha")
	}
}

func TestPublicAdminSettingsSection(t *testing.T) {
	h := newHarness(t)
	h.loginAdmin()
	pg := h.get("/admin/settings")
	wantStatus(t, pg, http.StatusOK)
	wantContains(t, pg.body, "Доступ к панели из интернета", "https://"+publicHost, "потенциальная угроза",
		`name="confirm"`, `name="password"`, "Только через SSH-туннель")

	// Without the checkbox, then with a wrong password: still off.
	for _, form := range []url.Values{
		{"enabled": {"1"}, "password": {testenv.AdminPassword}},
		{"enabled": {"1"}, "confirm": {"1"}, "password": {"wrong-password-1"}},
	} {
		res := h.submit("/admin/settings", "/admin/settings/public-admin", form)
		wantStatus(t, res, http.StatusSeeOther)
		wantStatus(t, h.follow(res), http.StatusOK)
		if h.env.Svc.PublicAdminEnabled(context.Background()) {
			t.Fatalf("turned on with %v", form)
		}
	}
	res := h.submit("/admin/settings", "/admin/settings/public-admin",
		url.Values{"enabled": {"1"}, "confirm": {"1"}, "password": {testenv.AdminPassword}})
	page := h.follow(res)
	wantContains(t, page.body, "Панель открыта из интернета", "Закрыть доступ из интернета")
	if !h.env.Svc.PublicAdminEnabled(context.Background()) {
		t.Fatal("not turned on")
	}
	// The checklist and the Connect AI page show the address.
	wantContains(t, h.get("/start").body, "без SSH-туннеля", "https://"+publicHost)
	wantContains(t, h.get("/connect").body, "без SSH-туннеля", "https://"+publicHost)

	off := h.follow(h.submit("/admin/settings", "/admin/settings/public-admin", url.Values{"enabled": {"0"}}))
	wantContains(t, off.body, "Доступ из интернета закрыт")
	if h.env.Svc.PublicAdminEnabled(context.Background()) {
		t.Fatal("not turned off")
	}
	wantNotContains(t, h.get("/connect").body, "без SSH-туннеля")
}

func TestPublicAdminSettingsAdminsOnly(t *testing.T) {
	h := newHarness(t)
	h.loginUser("alice@pp.test")
	wantNotContains(t, h.get("/admin/settings").body, "Доступ к панели из интернета")
	pg := h.submit("/", "/admin/settings/public-admin",
		url.Values{"enabled": {"1"}, "confirm": {"1"}, "password": {testenv.UserPassword}})
	wantStatus(t, pg, http.StatusNotFound)
	if h.env.Svc.PublicAdminEnabled(context.Background()) {
		t.Fatal("a user turned it on")
	}
}
