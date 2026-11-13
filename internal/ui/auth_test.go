package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"/":                      "/login",
		"/sites/abc":             "/login?next=%2Fsites%2Fabc",
		"/connect":               "/login?next=%2Fconnect",
		"/admin/users":           "/login?next=%2Fadmin%2Fusers",
		"/admin/audit?action=x":  "/login?next=%2Fadmin%2Faudit%3Faction%3Dx",
		"/no/such/page":          "/login?next=%2Fno%2Fsuch%2Fpage",
		"/sites/abc/slug-check":  "/login?next=%2Fsites%2Fabc%2Fslug-check",
		"/sites/abc/history?x=1": "/login?next=%2Fsites%2Fabc%2Fhistory%3Fx%3D1",
	}
	for path, want := range cases {
		p := h.get(path)
		wantStatus(t, p, http.StatusSeeOther)
		if p.location() != want {
			t.Errorf("%s redirects to %q, want %q", path, p.location(), want)
		}
	}
	wantStatus(t, h.get("/login"), http.StatusOK)
	wantStatus(t, h.get("/static/app.css"), http.StatusOK)
	inv := h.get("/invite/not-a-real-secret")
	wantStatus(t, inv, http.StatusNotFound)
	wantContains(t, inv.body, "Обратитесь к администратору")

	// POSTs without a session are redirected too and change nothing.
	p := h.post("/sites", url.Values{"title": {"x"}})
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != "/login" {
		t.Errorf("POST redirect = %q", p.location())
	}
	hx := h.get("/connect", "HX-Request", "true")
	wantStatus(t, hx, http.StatusUnauthorized)
	if hx.resp.Header.Get("HX-Redirect") == "" {
		t.Error("htmx request must get HX-Redirect")
	}
}

func TestLoginLogout(t *testing.T) {
	h := newHarness(t)
	h.env.User("alice@pp.test")

	for _, creds := range [][2]string{
		{"alice@pp.test", "wrong-password"},
		{"nobody@pp.test", "whatever-123"},
	} {
		p := h.login(creds[0], creds[1])
		wantStatus(t, p, http.StatusUnauthorized)
		wantContains(t, p.body, "Неверная почта или пароль.")
	}

	p := h.submit("/login?next=/connect", "/login", url.Values{
		"email": {"alice@pp.test"}, "password": {testenv.UserPassword}, "next": {"/connect"}, "altcha": {h.solve()},
	})
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != "/connect" {
		t.Fatalf("redirect after login = %q", p.location())
	}
	var sess *http.Cookie
	for _, c := range p.resp.Cookies() {
		if c.Name == sessionCookieName {
			sess = c
		}
	}
	if sess == nil || !sess.HttpOnly || sess.SameSite != http.SameSiteLaxMode || sess.Secure || sess.Domain != "" || sess.Path != "/" {
		t.Fatalf("session cookie attributes: %+v", sess)
	}

	home := h.get("/")
	wantStatus(t, home, http.StatusOK)
	wantContains(t, home.body, "alice@pp.test", "Мои сайты")
	// Already signed in: the login page sends you on.
	wantStatus(t, h.get("/login"), http.StatusSeeOther)

	out := h.submit("/", "/logout", nil)
	wantStatus(t, out, http.StatusSeeOther)
	if out.location() != "/login" {
		t.Fatalf("logout redirect = %q", out.location())
	}
	wantStatus(t, h.get("/"), http.StatusSeeOther)
	// The old cookie value no longer authenticates.
	if _, _, err := h.env.Svc.AuthenticateSession(context.Background(), sess.Value, "127.0.0.1"); err == nil {
		t.Fatal("session still valid after logout")
	}
}

func TestLoginOpenRedirectIsBlocked(t *testing.T) {
	h := newHarness(t)
	p := h.submit("/login", "/login", url.Values{
		"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword}, "next": {"//evil.example/x"},
		"altcha": {h.solve()},
	})
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != "/" {
		t.Fatalf("redirect = %q, want /", p.location())
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/sites/a?b=c":         "/sites/a?b=c",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
		"evil":                 "/",
		"/ok\r\nx":             "/",
		"/%2F%2Fevil":          "/%2F%2Fevil",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoginRateLimitMessage(t *testing.T) {
	h := newHarness(t)
	var p page
	for range 11 {
		p = h.login("x@pp.test", "wrong-password-1")
	}
	wantStatus(t, p, http.StatusTooManyRequests)
	wantContains(t, p.body, "Слишком много попыток", "мин.")
}

func TestInvite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	link, err := h.env.Svc.CreateUser(ctx, h.env.Admin, "new@pp.test", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	path := "/invite/" + testenv.InviteSecret(link.URL)

	form := wantStatusPage(t, h.get(path), http.StatusOK)
	wantContains(t, form, "new@pp.test", `minlength="10"`)

	short := h.submit(path, path, url.Values{"password": {"short"}, "password2": {"short"}, "altcha": {h.solve()}})
	wantStatus(t, short, http.StatusUnprocessableEntity)
	wantContains(t, short.body, "не меньше 10 символов")

	mismatch := h.submit(path, path, url.Values{"password": {"long-password-1"}, "password2": {"long-password-2"}, "altcha": {h.solve()}})
	wantStatus(t, mismatch, http.StatusUnprocessableEntity)
	wantContains(t, mismatch.body, "Пароли не совпадают")

	ok := h.submit(path, path, url.Values{"password": {"long-password-1"}, "password2": {"long-password-1"}, "altcha": {h.solve()}})
	wantStatus(t, ok, http.StatusSeeOther)
	home := h.get("/")
	wantStatus(t, home, http.StatusOK)
	wantContains(t, home.body, "new@pp.test")

	// The link is single-use.
	again := h.get(path)
	wantStatus(t, again, http.StatusNotFound)
	wantContains(t, again.body, "Ссылка не работает")
	used := h.submit("/", path, url.Values{"password": {"long-password-1"}, "password2": {"long-password-1"}})
	wantStatus(t, used, http.StatusNotFound)
}

func wantStatusPage(t *testing.T, p page, status int) string {
	t.Helper()
	wantStatus(t, p, status)
	return p.body
}

func TestCSRFProtection(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	tok := h.token("/")
	countSites := func() int {
		sites, _, err := h.env.Svc.ListSites(context.Background(), alice, testPage)
		if err != nil {
			t.Fatal(err)
		}
		return len(sites)
	}

	cases := []struct {
		name    string
		form    url.Values
		headers []string
	}{
		{"no token", url.Values{"title": {"A"}}, nil},
		{"wrong token", url.Values{"title": {"A"}, csrfField: {strings.Repeat("0", 64)}}, nil},
		{"foreign origin", url.Values{"title": {"A"}, csrfField: {tok}}, []string{"Origin", "https://evil.example"}},
		{"null origin", url.Values{"title": {"A"}, csrfField: {tok}}, []string{"Origin", "null"}},
		{"cross-site fetch", url.Values{"title": {"A"}, csrfField: {tok}}, []string{"Sec-Fetch-Site", "cross-site"}},
		{"wrong header", url.Values{"title": {"A"}, csrfField: {tok}}, []string{csrfHeader, "deadbeef"}},
	}
	for _, c := range cases {
		p := h.post("/sites", c.form, c.headers...)
		if p.status != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", c.name, p.status)
		}
		wantContains(t, p.body, "Запрос отклонён")
	}
	if n := countSites(); n != 0 {
		t.Fatalf("rejected requests created %d sites", n)
	}

	ok := h.post("/sites", url.Values{"title": {"A"}, csrfField: {tok}},
		"Origin", h.srv.URL, "Sec-Fetch-Site", "same-origin")
	wantStatus(t, ok, http.StatusSeeOther)
	hdr := h.post("/sites", url.Values{"title": {"B"}}, csrfHeader, tok)
	wantStatus(t, hdr, http.StatusSeeOther)
	if n := countSites(); n != 2 {
		t.Fatalf("sites = %d, want 2", n)
	}

	// Anonymous forms use the double-submit cookie.
	anon := newHarness(t)
	p := anon.post("/login", url.Values{"email": {testenv.AdminEmail}, "password": {testenv.AdminPassword}})
	wantStatus(t, p, http.StatusForbidden)
	jsonDeny := h.post("/sites", url.Values{"title": {"C"}}, "Accept", "application/json")
	wantStatus(t, jsonDeny, http.StatusForbidden)
	wantContains(t, jsonDeny.body, `"ok":false`)
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	h.loginAdmin()
	p := h.get("/connect")
	want := map[string]string{
		"Content-Security-Policy": csp,
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
		"Cache-Control":           "no-store",
	}
	for k, v := range want {
		if got := p.resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	// No inline scripts, handlers or style attributes anywhere in the page.
	wantNotContains(t, p.body, "<script>", " style=", " onclick=", " onload=")
	wantContains(t, p.body, `"allowEval":false`)
}
