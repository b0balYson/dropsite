package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/service"
)

// lockedBuffer collects log output written from server goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// newLoggedFlow is newFlow with the server log kept for inspection.
func newLoggedFlow(t *testing.T) (*flow, *lockedBuffer) {
	t.Helper()
	logs := &lockedBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := build(context.Background(), testConfig(t, t.TempDir()), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	srv := httptest.NewTLSServer(a.public)
	t.Cleanup(srv.Close)
	c := srv.Client()
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &flow{t: t, a: a, srv: srv, c: c}, logs
}

// consent opens the consent page for an authorization request with the
// given resource indicator.
func (f *flow) consent(clientID, resource string) (*reply, string) {
	f.t.Helper()
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"code_challenge": {challenge()}, "code_challenge_method": {"S256"}, "state": {"st-1"},
		"resource": {resource}, "scope": {"sites"},
	}
	return f.do(http.MethodGet, "/oauth/authorize?"+q.Encode(), "", nil)
}

// submitConsent posts the consent form of page.
func (f *flow) submitConsent(page string, extra url.Values) *reply {
	f.t.Helper()
	csrf, req := csrfRe.FindStringSubmatch(page), requestRe.FindStringSubmatch(page)
	if csrf == nil || req == nil {
		f.t.Fatalf("consent form fields missing: %.300s", page)
	}
	v := url.Values{"_csrf": {csrf[1]}, "request": {req[1]}}
	for k, vs := range extra {
		v[k] = vs
	}
	resp, body := f.form("/oauth/authorize", v)
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		f.t.Fatalf("consent submit: %d %.300s", resp.StatusCode, body)
	}
	return resp
}

func (f *flow) mcpSessionAt(path, token string) (*sdk.ClientSession, error) {
	hc := &http.Client{Transport: bearer{token: token, base: f.srv.Client().Transport}}
	tr := &sdk.StreamableClientTransport{Endpoint: f.srv.URL + path, HTTPClient: hc, DisableStandaloneSSE: true}
	return sdk.NewClient(&sdk.Implementation{Name: "flow", Version: "1"}, nil).Connect(context.Background(), tr, nil)
}

var (
	codeFieldRe = regexp.MustCompile(`name="code"`)
	// langHrefRe matches the language links, which repeat the request URL
	// the browser already shows in its address bar.
	langHrefRe = regexp.MustCompile(`href="/oauth/authorize\?[^"]*"`)
)

// TestConnectURLEndToEnd walks a chat client through OAuth discovery from a
// connect URL: 401 → path-aware protected resource metadata → server
// metadata → registration → authorization with the connect URL as resource
// (consent only, no code field) → token → MCP on the connect URL. The URL
// then stays an MCP alias but cannot grant again, and its code never shows
// up in the log or the page.
func TestConnectURLEndToEnd(t *testing.T) {
	f, logs := newLoggedFlow(t)
	ctx := context.Background()
	alice := f.user("alice@pp.test")
	link, err := f.a.svc.CreateConnectURL(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	code, ok := strings.CutPrefix(link.URL, "https://api.pp.test/mcp/c/")
	if !ok || len(code) != 13 {
		t.Fatalf("connect URL: %s", link.URL)
	}
	path := "/mcp/c/" + code

	// Discovery from the 401 of the connect URL.
	resp, _ := f.do(http.MethodPost, path, "application/json", strings.NewReader(`{}`))
	wantMeta := `resource_metadata="https://api.pp.test/.well-known/oauth-protected-resource` + path + `"`
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), wantMeta) {
		t.Fatalf("401 challenge: %d %v", resp.StatusCode, resp.Header)
	}
	resp, prm := f.do(http.MethodGet, "/.well-known/oauth-protected-resource"+path, "", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(prm, `"resource":"`+link.URL+`"`) ||
		!strings.Contains(prm, `"authorization_servers":["https://api.pp.test"]`) {
		t.Fatalf("protected resource metadata: %d %s", resp.StatusCode, prm)
	}
	// RFC 8414: the issuer has no path, so path insertion is the root document.
	_, asm := f.do(http.MethodGet, "/.well-known/oauth-authorization-server", "", nil)
	if !strings.Contains(asm, `"issuer":"https://api.pp.test"`) ||
		!strings.Contains(asm, `"authorization_endpoint":"https://api.pp.test/oauth/authorize"`) {
		t.Fatalf("server metadata: %s", asm)
	}
	// Anything that is not a code is not a connect URL.
	for _, p := range []string{"/mcp/c/not!a-code", "/mcp/c/" + strings.Repeat("A", 70)} {
		if r, _ := f.do(http.MethodPost, p, "application/json", strings.NewReader(`{}`)); r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d", p, r.StatusCode)
		}
		if r, _ := f.do(http.MethodGet, "/.well-known/oauth-protected-resource"+p, "", nil); r.StatusCode != http.StatusNotFound {
			t.Fatalf("metadata %s: %d", p, r.StatusCode)
		}
	}

	clientID := f.register()

	// The consent page asks only for consent and never shows the code.
	resp, page := f.consent(clientID, link.URL)
	if resp.StatusCode != http.StatusOK || codeFieldRe.MatchString(page) || strings.Contains(langHrefRe.ReplaceAllString(page, ""), code) ||
		!strings.Contains(page, "Подтвердите подключение") || !strings.Contains(page, "Test client") {
		t.Fatalf("link consent page: %d %.2000s", resp.StatusCode, page)
	}
	// Opening the page does not use the code: a second look is the same.
	if _, again := f.consent(clientID, link.URL); codeFieldRe.MatchString(again) {
		t.Fatal("the link was consumed by viewing the page")
	}
	loc, _ := url.Parse(f.submitConsent(page, url.Values{"action": {"allow"}}).Header.Get("Location"))
	if loc.Host != "client.example" || loc.Query().Get("code") == "" || loc.Query().Get("iss") != "https://api.pp.test" {
		t.Fatalf("approve redirect: %s", loc)
	}
	status, tk := f.token(url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "resource": {link.URL},
	})
	if status != http.StatusOK || tk.AccessToken == "" {
		t.Fatalf("token: %d %+v", status, tk)
	}

	// MCP on the connect URL and on the plain address with the same token.
	cs, err := f.mcpSessionAt(path, tk.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_sites", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_sites on the connect URL: %v %+v", err, res)
	}
	_ = cs.Close()
	if f.mcpStatus(tk.AccessToken) != http.StatusOK {
		t.Fatal("token for the connect URL rejected on /mcp")
	}
	// Refresh with the connect URL as resource still works after the code is used.
	status, tk2 := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.RefreshToken},
		"client_id": {clientID}, "resource": {link.URL}})
	if status != http.StatusOK || tk2.AccessToken == "" {
		t.Fatalf("refresh with the connect URL: %d %+v", status, tk2)
	}

	// The used link cannot grant again: the code field comes back with a
	// message that does not say why.
	_, page = f.consent(clientID, link.URL)
	if !codeFieldRe.MatchString(page) || !strings.Contains(page, "Ссылка для подключения недействительна") {
		t.Fatalf("used link page: %.2000s", page)
	}
	// The usual code still works on that page.
	loc, _ = url.Parse(f.submitConsent(page, url.Values{"action": {"allow"}, "code": {f.connectCode(alice)}}).Header.Get("Location"))
	if loc.Query().Get("code") == "" {
		t.Fatalf("code after a used link: %s", loc)
	}

	// A plain /mcp resource is today's flow: the code field.
	if _, plain := f.consent(clientID, "https://api.pp.test/mcp"); !codeFieldRe.MatchString(plain) {
		t.Fatal("plain resource lost the code field")
	}

	// One grant: the second authorization code was never exchanged.
	grants, err := f.a.svc.ListOAuthGrants(ctx, alice)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants: %d %v", len(grants), err)
	}
	if strings.Contains(logs.String(), code) {
		t.Fatalf("the connect code reached the log")
	}
	if !strings.Contains(logs.String(), `"api request"`) {
		t.Fatal("no access log captured")
	}
}

// TestConnectURLInvalidAndLockout: an unknown code in the resource falls
// back to the code field, and every such attempt counts toward the
// wrong-code limit shared with typed codes.
func TestConnectURLInvalidAndLockout(t *testing.T) {
	f := newFlow(t)
	clientID := f.register()
	bad := "https://api.pp.test/mcp/c/ABCDEFGHJKMNP"
	for i := range service.FailedCodesPerIP {
		_, page := f.consent(clientID, bad)
		if !codeFieldRe.MatchString(page) || !strings.Contains(page, "Ссылка для подключения недействительна") {
			t.Fatalf("attempt %d: %.1500s", i, page)
		}
	}
	resp, page := f.consent(clientID, bad)
	if !codeFieldRe.MatchString(page) || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("after the limit: %d %v", resp.StatusCode, resp.Header)
	}
	// A typed code is locked out as well.
	alice := f.user("alice@pp.test")
	_, page = f.consent(clientID, "https://api.pp.test/mcp")
	csrf, req := csrfRe.FindStringSubmatch(page), requestRe.FindStringSubmatch(page)
	resp, _ = f.form("/oauth/authorize", url.Values{"_csrf": {csrf[1]}, "request": {req[1]},
		"action": {"allow"}, "code": {f.connectCode(alice)}})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("typed code during lockout: %d", resp.StatusCode)
	}
}

// TestSessionAuthorizeOnPublicAdmin: with public access on, the consent page
// offers the public admin host, where the signed-in user approves without a
// code.
func TestSessionAuthorizeOnPublicAdmin(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	alice := f.user("alice@pp.test")
	clientID := f.register()

	_, page := f.consent(clientID, "https://api.pp.test/mcp")
	if strings.Contains(page, "/connect/authorize") {
		t.Fatal("admin link offered while public access is off")
	}
	if err := f.a.svc.SetPublicAdminCLI(ctx, true); err != nil {
		t.Fatal(err)
	}
	_, page = f.consent(clientID, "https://api.pp.test/mcp")
	m := regexp.MustCompile(`href="https://admin\.pp\.test/connect/authorize\?request=([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no admin link: %.2000s", page)
	}
	sealed, _ := url.QueryUnescape(strings.ReplaceAll(m[1], "&amp;", "&"))

	adm := httptest.NewServer(f.a.publicAdmin)
	defer adm.Close()
	login, err := f.a.svc.Login(ctx, "alice@pp.test", "user-password-1", "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body io.Reader, cookie bool) (*reply, string) {
		req, _ := http.NewRequestWithContext(ctx, method, adm.URL+path, body)
		req.Host = "admin.pp.test"
		if body != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "https://admin.pp.test")
		}
		if cookie {
			req.AddCookie(&http.Cookie{Name: "__Host-ds_session", Value: login.Secret})
		}
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return &reply{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
	}
	target := "/connect/authorize?" + url.Values{"request": {sealed}}.Encode()
	// Not signed in: to the login page, coming back afterwards.
	resp, _ := call(http.MethodGet, target, nil, false)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=%2Fconnect%2Fauthorize") {
		t.Fatalf("anonymous: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, page = call(http.MethodGet, target, nil, true)
	if resp.StatusCode != http.StatusOK || codeFieldRe.MatchString(page) || !strings.Contains(page, "alice@pp.test") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self' https://client.example") {
		t.Fatalf("session consent: %d %.2000s", resp.StatusCode, page)
	}
	csrf, req := csrfRe.FindStringSubmatch(page), requestRe.FindStringSubmatch(page)
	if csrf == nil || req == nil {
		t.Fatal("session consent form fields missing")
	}
	form := url.Values{"_csrf": {csrf[1]}, "request": {req[1]}, "action": {"allow"}}
	resp, body := call(http.MethodPost, "/connect/authorize", strings.NewReader(form.Encode()), true)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Host != "client.example" || loc.Query().Get("code") == "" ||
		loc.Query().Get("iss") != "https://api.pp.test" {
		t.Fatalf("session approve: %d %s %.500s", resp.StatusCode, loc, body)
	}
	tk := f.exchange(clientID, loc)
	if f.mcpStatus(tk.AccessToken) != http.StatusOK {
		t.Fatal("token from the session approval rejected")
	}
	grants, err := f.a.svc.ListOAuthGrants(ctx, alice)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants: %v %v", grants, err)
	}
	// A forged request is refused.
	resp, _ = call(http.MethodGet, "/connect/authorize?request=forged", nil, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("forged request: %d", resp.StatusCode)
	}
	// The loopback admin listener has no authorization server: 404.
	loop := httptest.NewServer(f.a.admin)
	defer loop.Close()
	r2, _ := http.NewRequestWithContext(ctx, http.MethodGet, loop.URL+target, nil)
	r2.AddCookie(&http.Cookie{Name: "ds_session", Value: login.Secret})
	lr, err := http.DefaultClient.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	lr.Body.Close()
	if lr.StatusCode != http.StatusNotFound {
		t.Fatalf("loopback listener: %d", lr.StatusCode)
	}
}

func TestConnectURLCmd(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	a, err := build(ctx, cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	_ = a.db.Close()
	t.Setenv("BASE_DOMAIN", "pp.test")
	t.Setenv("ADMIN_EMAIL", "admin@pp.test")
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("SITES_DIR", cfg.SitesDir)
	t.Setenv("STATE_DIR", cfg.StateDir)

	var out, notes bytes.Buffer
	if err := connectURLCmd(ctx, nil, &out, &notes); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	if !regexp.MustCompile(`^https://api\.pp\.test/mcp/c/[0-9A-Z]{13}$`).MatchString(line) {
		t.Fatalf("stdout: %q", out.String())
	}
	if strings.Contains(notes.String(), line[len(line)-13:]) || !strings.Contains(notes.String(), "admin@pp.test") {
		t.Fatalf("notes: %s", notes.String())
	}
	if err := connectURLCmd(ctx, []string{"nobody@pp.test"}, &bytes.Buffer{}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "no user") {
		t.Fatalf("unknown user: %v", err)
	}
	if err := connectURLCmd(ctx, []string{"a", "b"}, &bytes.Buffer{}, io.Discard); !errors.Is(err, errUsage) {
		t.Fatalf("two arguments: %v", err)
	}
}

// TestConnectURLLoopbackClient: a command-line client (registered with
// http://localhost/callback, listening on a run-time port, like Claude Code)
// connects with a connect URL.
func TestConnectURLLoopbackClient(t *testing.T) {
	f := newFlow(t)
	ctx := context.Background()
	alice := f.user("alice@pp.test")
	link, err := f.a.svc.CreateConnectURL(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := f.do(http.MethodPost, "/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/callback"],"client_name":"Claude Code","token_endpoint_auth_method":"none"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	clientID := regexp.MustCompile(`"client_id":"([^"]+)"`).FindStringSubmatch(body)[1]
	const cb = "http://localhost:53123/callback"
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {cb},
		"code_challenge": {challenge()}, "code_challenge_method": {"S256"}, "state": {"st-2"},
		"resource": {link.URL},
	}
	resp, page := f.do(http.MethodGet, "/oauth/authorize?"+q.Encode(), "", nil)
	if resp.StatusCode != http.StatusOK || codeFieldRe.MatchString(page) ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self' http://localhost:53123") {
		t.Fatalf("loopback consent: %d %v", resp.StatusCode, resp.Header)
	}
	loc, _ := url.Parse(f.submitConsent(page, url.Values{"action": {"allow"}}).Header.Get("Location"))
	if loc.Host != "localhost:53123" || loc.Query().Get("code") == "" {
		t.Fatalf("loopback redirect: %s", loc)
	}
	status, tk := f.token(url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"client_id": {clientID}, "redirect_uri": {cb}, "resource": {link.URL},
	})
	if status != http.StatusOK {
		t.Fatalf("token: %d %+v", status, tk)
	}
	cs, err := f.mcpSessionAt(strings.TrimPrefix(link.URL, "https://api.pp.test"), tk.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.Close()
}
