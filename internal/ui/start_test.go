package ui

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/setupcheck"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// fakeHTTPS answers the HTTPS probe of the checklist without a network.
type fakeHTTPS struct {
	status atomic.Int32 // 0 means "connection refused"
	calls  atomic.Int32
}

func (f *fakeHTTPS) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	code := int(f.status.Load())
	if code == 0 {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}, Request: r}, nil
}

// newStartHarness is a server whose addresses are known (SERVER_IPS) and
// whose HTTPS probe answers status (0: unreachable).
func newStartHarness(t *testing.T, status int) (*harness, *fakeHTTPS) {
	t.Helper()
	tr := &fakeHTTPS{}
	tr.status.Store(int32(status))
	h := newHarness(t, testenv.WithEnv("SERVER_IPS", testenv.ServerIP), testenv.WithSetupTransport(tr))
	return h, tr
}

func stepState(body, step string) string {
	i := strings.Index(body, `id="step-`+step+`"`)
	if i < 0 {
		return ""
	}
	j := strings.LastIndex(body[:i], "setup-step--")
	if j < 0 {
		return ""
	}
	rest := body[j+len("setup-step--"):]
	return rest[:strings.IndexAny(rest, `" `)]
}

func TestStartIsForAdminsOnly(t *testing.T) {
	h, tr := newStartHarness(t, http.StatusOK)
	h.loginUser("bob@pp.test")
	for _, p := range []string{"/start", "/start/checks", "/start/summary"} {
		wantStatus(t, h.get(p), http.StatusNotFound)
	}
	home := h.get("/")
	wantNotContains(t, home.body, `href="/start"`, `hx-get="/start/summary"`)
	conn := h.get("/connect")
	wantNotContains(t, conn.body, `href="/start`)
	wantContains(t, conn.body, h.mustLocale("ru").T("connect.trouble.askadmin"))
	if tr.calls.Load() != 0 {
		t.Fatal("a user must never trigger the network checks")
	}
}

func TestStartPageLoadsChecksLazily(t *testing.T) {
	h, tr := newStartHarness(t, http.StatusOK)
	h.loginAdmin()
	p := h.get("/start")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, `hx-get="/start/checks"`, `aria-busy="true"`, `id="step-chat"`, `id="step-team"`, `aria-current="page"><span class="side-link__text">`)
	if tr.calls.Load() != 0 {
		t.Fatal("the page must render without waiting for the checks")
	}
	home := h.get("/")
	wantContains(t, home.body, `href="/start"`, `hx-get="/start/summary"`)
}

func TestStartChecksAllDone(t *testing.T) {
	h, tr := newStartHarness(t, http.StatusOK)
	h.env.DNS.Set("*."+testenv.BaseDomain, testenv.ServerIP)
	h.loginAdmin()

	p := h.get("/start/checks")
	wantStatus(t, p, http.StatusOK)
	for step, want := range map[string]string{"domain": "done", "https": "done", "chat": "todo", "site": "todo"} {
		if got := stepState(p.body, step); got != want {
			t.Errorf("step %s = %q, want %q", step, got, want)
		}
	}
	wantContains(t, p.body, `<span class="mono">2/4</span>`, `href="/connect"`, `href="/#new-site"`)
	wantNotContains(t, p.body, `action="/start/hide"`)
	if tr.calls.Load() != 1 {
		t.Fatalf("probe calls = %d, want 1", tr.calls.Load())
	}

	h.createSite(h.env.Admin, "Promo", "promo")
	h.env.Token(h.env.Admin, "laptop")
	ready := h.get("/start/checks")
	wantContains(t, ready.body, `<span class="mono">4/4</span>`, `action="/start/hide"`, "setup-status--ready")
	if tr.calls.Load() != 1 {
		t.Fatal("a second check within a minute must reuse the cached result")
	}
	summary := h.get("/start/summary")
	wantContains(t, summary.body, "setup-card--ready", `action="/start/hide"`)

	// The page itself now shows the cached result without a placeholder.
	page := h.get("/start")
	wantNotContains(t, page.body, `hx-get="/start/checks" hx-trigger="load"`)
	wantContains(t, page.body, `<span class="mono">4/4</span>`)
}

func TestStartChecksExplainDNSProblems(t *testing.T) {
	h, tr := newStartHarness(t, http.StatusOK)
	h.env.DNS.Set(testenv.BaseDomain, "198.51.100.7") // old hosting
	h.loginAdmin()
	p := h.get("/start/checks")
	if got := stepState(p.body, "domain"); got != "problem" {
		t.Fatalf("domain = %q, want problem", got)
	}
	if got := stepState(p.body, "https"); got != "todo" {
		t.Fatalf("https = %q, want todo while DNS is wrong", got)
	}
	ru := h.mustLocale("ru")
	wantContains(t, p.body,
		ru.T("setup.domain.reason.points_elsewhere"),
		"198.51.100.7",
		`id="rec-0">`+testenv.ServerIP+`</code>`,
		`data-copy="#rec-1"`,
		ru.T("setup.https.reason.dns_pending"),
	)
	if tr.calls.Load() != 0 {
		t.Fatal("HTTPS is not probed while the domain is not ready")
	}
}

func TestStartChecksHTTPSProblems(t *testing.T) {
	cases := []struct {
		status int
		key    string
	}{
		{0, "setup.https.reason.unreachable"},
		{http.StatusBadGateway, "setup.https.reason.bad_status"},
	}
	for _, tc := range cases {
		h, _ := newStartHarness(t, tc.status)
		h.env.DNS.Set("*."+testenv.BaseDomain, testenv.ServerIP)
		h.loginAdmin()
		p := h.get("/start/checks")
		if got := stepState(p.body, "https"); got != "problem" {
			t.Fatalf("status %d: https = %q, want problem", tc.status, got)
		}
		host := h.env.Config.APIHost
		wantContains(t, p.body, h.mustLocale("ru").T(tc.key, "host", host, "status", tc.status), setupcheck.ProbePath)
	}
}

func TestStartRecheckBypassesCache(t *testing.T) {
	h, tr := newStartHarness(t, http.StatusOK)
	h.loginAdmin()
	first := h.get("/start/checks")
	if got := stepState(first.body, "domain"); got != "todo" {
		t.Fatalf("domain = %q, want todo (no wildcard yet)", got)
	}
	h.env.DNS.Set("*."+testenv.BaseDomain, testenv.ServerIP)
	if got := stepState(h.get("/start/checks").body, "domain"); got != "todo" {
		t.Fatal("a plain reload within a minute is served from the cache")
	}
	// The service clock decides the minimum interval between forced checks.
	h.env.Clock.Advance(setupcheck.MinInterval)
	again := h.get("/start/checks?fresh=1")
	if got := stepState(again.body, "domain"); got != "done" {
		t.Fatalf("after Check again domain = %q, want done", got)
	}
	if tr.calls.Load() != 1 {
		t.Fatalf("probe calls = %d", tr.calls.Load())
	}
	// Without JavaScript the same button reloads the page with fresh results.
	noJS := h.get("/start?fresh=1")
	wantContains(t, noJS.body, `aria-busy="false"`)
}

func TestStartHideAndShow(t *testing.T) {
	h, _ := newStartHarness(t, http.StatusOK)
	h.loginAdmin()
	p := h.submit("/start", "/start/hide", nil)
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != "/" {
		t.Fatalf("hide redirects to %q", p.location())
	}
	home := h.follow(p)
	wantNotContains(t, home.body, `href="/start"`, `hx-get="/start/summary"`)
	wantContains(t, home.body, h.mustLocale("ru").T("setup.hidden"))
	wantStatus(t, h.get("/start/summary"), http.StatusNoContent)

	settings := h.get("/admin/settings")
	wantContains(t, settings.body, `action="/start/show"`)
	page := h.get("/start")
	wantContains(t, page.body, `action="/start/show"`)
	wantNotContains(t, page.body, `class="setup-dismiss"`)

	back := h.submit("/admin/settings", "/start/show", nil)
	if back.location() != "/start" {
		t.Fatalf("show redirects to %q", back.location())
	}
	wantContains(t, h.get("/").body, `href="/start"`)
	wantNotContains(t, h.get("/admin/settings").body, `action="/start/show"`)

	rows, _, err := h.env.Svc.ListAudit(context.Background(), h.env.Admin, service.AuditFilter{Page: service.Page{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range rows {
		actions = append(actions, r.Action)
	}
	if got := strings.Join(actions, ","); !strings.Contains(got, "setup.show") || !strings.Contains(got, "setup.hide") {
		t.Fatalf("audit actions = %s", got)
	}
}

func TestStartHideNeedsAdminAndCSRF(t *testing.T) {
	h, _ := newStartHarness(t, http.StatusOK)
	h.loginAdmin()
	wantStatus(t, h.post("/start/hide", url.Values{}), http.StatusForbidden)
	if h.env.Svc.SetupHidden(context.Background()) {
		t.Fatal("a request without the CSRF token must not hide the checklist")
	}
	u := h.env.User("eve@pp.test")
	if err := h.env.Svc.SetSetupHidden(context.Background(), u, true); err == nil {
		t.Fatal("a user cannot hide the checklist")
	}
}

func TestStartOptionalSteps(t *testing.T) {
	h, _ := newStartHarness(t, http.StatusOK)
	h.loginAdmin()
	p := h.get("/start")
	for _, s := range []string{"team", "requests"} {
		if got := stepState(p.body, s); got != "todo" {
			t.Errorf("%s = %q, want todo", s, got)
		}
	}
	h.env.User("colleague@pp.test")
	if got := stepState(h.get("/start").body, "team"); got != "done" {
		t.Errorf("team = %q after inviting a user", got)
	}
}

func TestConnectPageGuides(t *testing.T) {
	h, _ := newStartHarness(t, http.StatusOK)
	h.loginAdmin()
	p := h.get("/connect")
	wantStatus(t, p, http.StatusOK)
	mcp := h.env.Svc.MCPResource()
	meta := h.env.Svc.APIURL() + setupcheck.ProbePath
	wantContains(t, p.body,
		`id="mcp-url-gpt">`+mcp+`</code>`, `data-copy="#mcp-url-gpt"`,
		`id="mcp-url-claude">`+mcp+`</code>`, `data-copy="#mcp-url-claude"`,
		"<strong>Developer mode</strong>", "<strong>Add custom connector</strong>",
		`href="#connect-block"`, `id="trouble-h"`, `href="`+meta+`"`, `href="/start#step-domain"`,
		h.mustLocale("ru").T("connect.first.check"),
	)
	// The one-click connect code flow stays.
	wantContains(t, p.body, `hx-post="/connect/code"`)
}

func TestEmphEscapes(t *testing.T) {
	cases := map[string]string{
		"Open **Settings** <b>now</b>": "Open <strong>Settings</strong> &lt;b&gt;now&lt;/b&gt;",
		"a **b":                        "a **b",
		"**<x>**":                      "<strong>&lt;x&gt;</strong>",
	}
	for in, want := range cases {
		if got := string(emph(in)); got != want {
			t.Errorf("emph(%q) = %q, want %q", in, got, want)
		}
	}
	if recordHost("@", "pp.test") != "pp.test" || recordHost("*", "pp.test") != "*.pp.test" {
		t.Error("recordHost")
	}
}
