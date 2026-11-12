package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestUnknownPathsAre404InAPIFormat(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/", "/internal/healthz", "/api/v2/me", "/api/v1/nope", "/api/v1/sites/x/unknown", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			wantError(t, h.do(http.MethodGet, path, h.adminToken, nil), http.StatusNotFound, domain.CodeNotFound)
			wantError(t, h.do(http.MethodGet, path, "", nil), http.StatusNotFound, domain.CodeNotFound)
		})
	}
}

func TestWrongMethodIs405InAPIFormat(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPut, "/api/v1/sites", h.adminToken, nil)
	wantError(t, rec, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "GET, HEAD, POST" {
		t.Fatalf("Allow %q", got)
	}
	rec = h.do(http.MethodGet, "/api/v1/tokens/1", "", nil)
	wantError(t, rec, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "DELETE" {
		t.Fatalf("Allow %q", got)
	}
}

func TestResponseHeaders(t *testing.T) {
	h := newHarness(t)
	for _, rec := range []*httptest.ResponseRecorder{
		h.do(http.MethodGet, "/api/v1/me", h.adminToken, nil),
		h.do(http.MethodGet, "/api/v1/me", "", nil),
		h.do(http.MethodGet, "/nowhere", "", nil),
	} {
		for k, v := range map[string]string{
			"Content-Type":           "application/json; charset=utf-8",
			"X-Content-Type-Options": "nosniff",
			"Cache-Control":          "no-store",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s = %q, want %q", k, got, v)
			}
		}
		if rec.Header().Get("X-Request-Id") == "" {
			t.Error("missing X-Request-Id")
		}
	}
}

func TestMe(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	h.createSite(tok, "One", "")
	rec := h.do(http.MethodGet, "/api/v1/me", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	me := decodeAs[meDTO](t, rec)
	if me.User.Email != "bob@pp.test" || me.User.Role != "user" || me.User.Status != "active" || me.SiteCount != 1 {
		t.Fatalf("me %+v", me)
	}
	if me.Limits.MaxSitesPerUser != 10 || me.Limits.KeepReleases != 5 || me.Limits.MaxSiteBytes != 50<<20 {
		t.Fatalf("limits %+v", me.Limits)
	}
	if me.User.CreatedAt.Location() != time.UTC {
		t.Fatal("time not UTC")
	}
	if !strings.Contains(rec.Body.String(), `"created_at":"2026-11-12T10:00:00Z"`) {
		t.Fatalf("time format: %s", rec.Body.String())
	}
}

func TestEveryProtectedRouteRequiresToken(t *testing.T) {
	h := newHarness(t)
	session := h.env.Login("admin@pp.test", "admin-password")
	for _, rt := range (&api{}).routes() {
		if rt.Public {
			continue
		}
		path := Prefix + strings.NewReplacer("{ref}", "abc", "{id}", "1").Replace(rt.Path)
		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			for name, opt := range map[string]reqOpt{
				"none":         func(*http.Request) {},
				"basic":        withHeader("Authorization", "Basic YTpi"),
				"empty bearer": withHeader("Authorization", "Bearer "),
				"cookie only": func(r *http.Request) {
					r.AddCookie(&http.Cookie{Name: "ds_session", Value: session})
					r.AddCookie(&http.Cookie{Name: "session", Value: h.adminToken})
				},
			} {
				rec := h.do(rt.Method, path, "", nil, opt)
				wantError(t, rec, http.StatusUnauthorized, domain.CodeUnauthorized)
				if !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
					t.Fatalf("%s: WWW-Authenticate %q", name, rec.Header().Get("WWW-Authenticate"))
				}
			}
		})
	}
}

func TestInvalidRevokedAndBlockedTokens(t *testing.T) {
	h := newHarness(t)
	wantError(t, h.do(http.MethodGet, "/api/v1/me", "ds_not-a-real-token", nil), http.StatusUnauthorized, domain.CodeUnauthorized)
	wantError(t, h.do(http.MethodGet, "/api/v1/me", "garbage", nil), http.StatusUnauthorized, domain.CodeUnauthorized)

	carol, tok := h.user("carol@pp.test")
	wantStatus(t, h.do(http.MethodGet, "/api/v1/me", tok, nil), http.StatusOK)
	rec := h.do(http.MethodPatch, "/api/v1/admin/users/"+idString(carol.User.ID), h.adminToken, map[string]any{"blocked": true})
	wantStatus(t, rec, http.StatusOK)
	if u := decodeAs[inviteDTO](t, rec); u.User.Status != "blocked" {
		t.Fatalf("status %q", u.User.Status)
	}
	wantError(t, h.do(http.MethodGet, "/api/v1/me", tok, nil), http.StatusUnauthorized, domain.CodeUnauthorized)
}

func TestTokenBruteForceIsRateLimited(t *testing.T) {
	h := newHarness(t)
	var rec *httptest.ResponseRecorder
	for range 31 {
		rec = h.do(http.MethodGet, "/api/v1/me", "ds_wrong", nil)
	}
	wantError(t, rec, http.StatusTooManyRequests, domain.CodeRateLimited)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct", "203.0.113.5:1234", nil, "203.0.113.5"},
		{"spoofed xff from untrusted peer", "203.0.113.5:1234", []string{"1.2.3.4"}, "203.0.113.5"},
		{"trusted proxy", "10.0.0.2:80", []string{"198.51.100.7"}, "198.51.100.7"},
		{"rightmost untrusted wins", "10.0.0.2:80", []string{"1.1.1.1, 198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
		{"multiple headers", "10.0.0.2:80", []string{"1.1.1.1", "198.51.100.8"}, "198.51.100.8"},
		{"all trusted", "10.0.0.2:80", []string{"10.1.1.1"}, "10.1.1.1"},
		{"garbage hop", "10.0.0.2:80", []string{"1.1.1.1, not-an-ip"}, "10.0.0.2"},
		{"no xff", "10.0.0.2:80", nil, "10.0.0.2"},
		{"ipv6 trusted", "[::1]:80", []string{"2001:db8::1"}, "2001:db8::1"},
		{"mapped v4", "[::ffff:203.0.113.9]:80", nil, "203.0.113.9"},
		{"unparseable remote", "pipe", nil, "pipe"},
		{"remote without port", "203.0.113.5", nil, "203.0.113.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(r, trusted); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSpoofedForwardedForIsNotAudited(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("dave@pp.test")
	// httptest requests come from 192.0.2.1, which is not a trusted proxy.
	h.do(http.MethodPost, "/api/v1/sites", tok, map[string]string{"title": "Spoof"}, withHeader("X-Forwarded-For", "6.6.6.6"))
	trusted := func(r *http.Request) { r.RemoteAddr = "127.0.0.1:5555" }
	h.do(http.MethodPost, "/api/v1/sites", tok, map[string]string{"title": "Proxied"}, trusted, withHeader("X-Forwarded-For", "6.6.6.6, 127.0.0.1"))

	rec := h.do(http.MethodGet, "/api/v1/admin/audit?action=site.create", h.adminToken, nil)
	items := decodeAs[list[auditDTO]](t, rec).Items
	ips := map[string]string{}
	for _, it := range items {
		ips[it.Meta["title"].(string)] = it.IP
	}
	if ips["Spoof"] != "192.0.2.1" || ips["Proxied"] != "6.6.6.6" {
		t.Fatalf("audited IPs %v", ips)
	}
}

func TestInternalErrorsAreHidden(t *testing.T) {
	logs := &logBuffer{}
	a := &api{log: slog.New(slog.NewJSONHandler(logs, nil))}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r = r.WithContext(context.WithValue(r.Context(), infoKey, &requestInfo{id: "req-1"}))
	rec := httptest.NewRecorder()
	a.writeError(rec, r, errors.New("open /secret/state.db: disk on fire"))
	p := wantError(t, rec, http.StatusInternalServerError, domain.CodeInternal)
	if p.Message != "internal error" || p.Details != nil || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("leaked: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "disk on fire") || !strings.Contains(logs.String(), "req-1") {
		t.Fatalf("cause not logged: %s", logs.String())
	}

	rec = httptest.NewRecorder()
	a.writeError(rec, r, domain.E(domain.KindInternal, domain.CodeInternal, "x").WithParams(map[string]any{"path": "/secret"}))
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("internal details leaked: %s", rec.Body.String())
	}
}

func TestPanicIsRecovered(t *testing.T) {
	h := newHarness(t)
	a := &api{svc: h.env.Svc, log: slog.New(slog.NewJSONHandler(h.logs, nil))}
	handler := a.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	wantError(t, rec, http.StatusInternalServerError, domain.CodeInternal)
	if !strings.Contains(h.logs.String(), "boom") {
		t.Fatal("panic not logged")
	}

	abort := a.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // sentinel identity
			t.Fatalf("recovered %v", v)
		}
	}()
	abort.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestStatusMappingAndRetryAfter(t *testing.T) {
	cases := map[domain.Kind]int{
		domain.KindNotFound: 404, domain.KindInvalid: 400, domain.KindConflict: 409, domain.KindLimit: 403,
		domain.KindTooLarge: 413, domain.KindRateLimited: 429, domain.KindUnauthorized: 401, domain.KindInternal: 500,
	}
	for kind, status := range cases {
		if got := statusFor(domain.E(kind, "c", "m")); got != status {
			t.Errorf("kind %d: %d, want %d", kind, got, status)
		}
	}
	for d, want := range map[time.Duration]string{0: "1", 1500 * time.Millisecond: "2", time.Minute: "60", time.Nanosecond: "1"} {
		if got := retryAfterSeconds(domain.ErrRateLimited.WithRetryAfter(d)); got != want {
			t.Errorf("%v: %s, want %s", d, got, want)
		}
	}
}

func TestAccessLog(t *testing.T) {
	h := newHarness(t)
	bob, tok := h.user("bob@pp.test")
	h.do(http.MethodGet, "/api/v1/sites/abc", tok, nil)
	logs := h.logs.String()
	for _, want := range []string{
		`"msg":"api request"`, `"route":"GET /sites/{ref}"`, `"status":404`,
		`"user_id":` + idString(bob.User.ID), `"ip":"192.0.2.1"`, `"method":"GET"`, `"duration_ms"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %s:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, tok) {
		t.Fatal("token leaked into logs")
	}
}

func TestJSONBodyValidation(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	cases := map[string]any{
		"empty":         "",
		"malformed":     "{",
		"unknown field": `{"title":"x","extra":1}`,
		"wrong type":    `{"title":5}`,
		"trailing":      `{"title":"x"} {}`,
		"too large":     `{"title":"` + strings.Repeat("a", maxJSONBody) + `"}`,
		"array":         `[1]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			wantError(t, h.do(http.MethodPost, "/api/v1/sites", tok, body), http.StatusBadRequest, domain.CodeInvalidInput)
		})
	}
}

func TestPagination(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	for _, title := range []string{"A", "B", "C"} {
		h.createSite(tok, title, "")
	}
	rec := h.do(http.MethodGet, "/api/v1/sites?limit=2&offset=1", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	l := decodeAs[list[siteItemDTO]](t, rec)
	if l.Total != 3 || len(l.Items) != 2 {
		t.Fatalf("list %+v", l)
	}
	for _, q := range []string{"limit=0", "limit=201", "limit=x", "offset=-1", "offset=y"} {
		wantError(t, h.do(http.MethodGet, "/api/v1/sites?"+q, tok, nil), http.StatusBadRequest, domain.CodeInvalidInput)
	}
	_, other := h.user("empty@pp.test")
	rec = h.do(http.MethodGet, "/api/v1/sites", other, nil)
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("items must be an array: %s", rec.Body.String())
	}
}
