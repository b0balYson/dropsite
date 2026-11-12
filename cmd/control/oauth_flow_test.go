package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// flow drives the public listener like a chat client and a browser would.
type flow struct {
	t   *testing.T
	a   *app
	srv *httptest.Server
	c   *http.Client
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	a, err := build(context.Background(), testConfig(t, t.TempDir()), quietLog())
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
	return &flow{t: t, a: a, srv: srv, c: c}
}

// reply is what the tests read from a response (the body is read and closed).
type reply struct {
	StatusCode int
	Header     http.Header
}

func (f *flow) do(method, path, ctype string, body io.Reader, hdr ...string) (*reply, string) {
	f.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.srv.URL+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := f.c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return &reply{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

func (f *flow) form(path string, v url.Values) (*reply, string) {
	f.t.Helper()
	return f.do(http.MethodPost, path, "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
}

func (f *flow) user(email string) service.Actor {
	f.t.Helper()
	ctx := context.Background()
	inv, err := f.a.svc.CreateUser(ctx, adminActor(f.t, f.a), email, domain.RoleUser)
	if err != nil {
		f.t.Fatal(err)
	}
	u, err := f.a.svc.RedeemInvite(ctx, inv.URL[strings.LastIndex(inv.URL, "/")+1:], "user-password-1", "127.0.0.1")
	if err != nil {
		f.t.Fatal(err)
	}
	return service.Actor{User: u}
}

func (f *flow) connectCode(a service.Actor) string {
	f.t.Helper()
	c, err := f.a.svc.CreateConnectCode(context.Background(), a)
	if err != nil {
		f.t.Fatal(err)
	}
	return c.Code
}

var (
	csrfRe    = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)
	requestRe = regexp.MustCompile(`name="request" value="([^"]+)"`)
)

const (
	redirectURI = "https://client.example/cb"
	verifier    = "flow-verifier-0123456789-0123456789-0123456789"
)

func challenge() string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *flow) register() string {
	f.t.Helper()
	resp, body := f.do(http.MethodPost, "/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["`+redirectURI+`"],"client_name":"Test client","token_endpoint_auth_method":"none"}`))
	if resp.StatusCode != http.StatusCreated {
		f.t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal([]byte(body), &reg)
	return reg.ClientID
}

// authorize opens the consent page and submits it with a connect code.
func (f *flow) authorize(clientID, code, action string) *reply {
	f.t.Helper()
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"code_challenge": {challenge()}, "code_challenge_method": {"S256"}, "state": {"st-1"},
		"resource": {"https://api.pp.test/mcp"}, "scope": {"sites"},
	}
	resp, page := f.do(http.MethodGet, "/oauth/authorize?"+q.Encode(), "", nil)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("authorize page: %d %s", resp.StatusCode, page)
	}
	for _, want := range []string{"Test client", "client.<wbr>example", "XXXX-XXXX-XXXXX"} {
		if !strings.Contains(page, want) {
			f.t.Fatalf("consent page lacks %q", want)
		}
	}
	if strings.Contains(page, `type="password"`) {
		f.t.Fatal("consent page asks for a password")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self' https://client.example") {
		f.t.Fatalf("consent headers: %v", resp.Header)
	}
	csrf, req := csrfRe.FindStringSubmatch(page), requestRe.FindStringSubmatch(page)
	if csrf == nil || req == nil {
		f.t.Fatal("consent form fields missing")
	}
	resp, body := f.form("/oauth/authorize", url.Values{
		"_csrf": {csrf[1]}, "request": {strings.ReplaceAll(req[1], "&#43;", "+")}, "code": {code}, "action": {action},
	})
	if resp.StatusCode != http.StatusSeeOther {
		f.t.Fatalf("consent submit: %d %.500s", resp.StatusCode, body)
	}
	return resp
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

func (f *flow) token(v url.Values) (int, tokens) {
	f.t.Helper()
	resp, body := f.form("/oauth/token", v)
	var tk tokens
	_ = json.Unmarshal([]byte(body), &tk)
	if resp.Header.Get("Cache-Control") != "no-store" {
		f.t.Fatal("token response is cacheable")
	}
	return resp.StatusCode, tk
}

func (f *flow) exchange(clientID string, loc *url.URL) tokens {
	f.t.Helper()
	status, tk := f.token(url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "resource": {"https://api.pp.test/mcp"},
	})
	if status != http.StatusOK || tk.TokenType != "Bearer" || tk.ExpiresIn != 3600 {
		f.t.Fatalf("token: %d %+v", status, tk)
	}
	return tk
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// mcpSession connects the go-sdk client to /mcp with a bearer token.
func (f *flow) mcpSession(token string) (*sdk.ClientSession, error) {
	hc := &http.Client{Transport: bearer{token: token, base: f.srv.Client().Transport}}
	tr := &sdk.StreamableClientTransport{Endpoint: f.srv.URL + "/mcp", HTTPClient: hc, DisableStandaloneSSE: true}
	return sdk.NewClient(&sdk.Implementation{Name: "flow", Version: "1"}, nil).Connect(context.Background(), tr, nil)
}

func (f *flow) mcpStatus(token string) int {
	f.t.Helper()
	resp, _ := f.do(http.MethodPost, "/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
		"Authorization", "Bearer "+token, "Accept", "application/json, text/event-stream")
	return resp.StatusCode
}

func TestOAuthFlowEndToEnd(t *testing.T) {
	f := newFlow(t)
	alice := f.user("alice@pp.test")

	// Discovery from a 401.
	resp, _ := f.do(http.MethodPost, "/mcp", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(resp.Header.Get("WWW-Authenticate"), `resource_metadata="https://api.pp.test/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("401 challenge: %d %v", resp.StatusCode, resp.Header)
	}
	_, prm := f.do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", nil)
	if !strings.Contains(prm, `"resource":"https://api.pp.test/mcp"`) || !strings.Contains(prm, `"authorization_servers":["https://api.pp.test"]`) {
		t.Fatalf("protected resource metadata: %s", prm)
	}
	_, asm := f.do(http.MethodGet, "/.well-known/oauth-authorization-server", "", nil)
	for _, want := range []string{`"code_challenge_methods_supported":["S256"]`, `"client_id_metadata_document_supported":true`,
		`"token_endpoint":"https://api.pp.test/oauth/token"`, `"authorization_response_iss_parameter_supported":true`} {
		if !strings.Contains(asm, want) {
			t.Fatalf("server metadata lacks %s: %s", want, asm)
		}
	}

	clientID := f.register()

	// Denial and a wrong code.
	loc, _ := url.Parse(f.authorize(clientID, "", "deny").Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("iss") != "https://api.pp.test" || loc.Query().Get("state") != "st-1" {
		t.Fatalf("deny redirect: %s", loc)
	}

	// Approval with a connect code.
	loc, _ = url.Parse(f.authorize(clientID, f.connectCode(alice), "allow").Header.Get("Location"))
	if loc.Host != "client.example" || loc.Query().Get("code") == "" || loc.Query().Get("iss") != "https://api.pp.test" {
		t.Fatalf("approve redirect: %s", loc)
	}
	tk := f.exchange(clientID, loc)

	// MCP with the access token.
	cs, err := f.mcpSession(tk.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "deploy_files", Arguments: map[string]any{
		"title": "From chat", "files": []map[string]string{{"path": "index.html", "content": "<h1>chat</h1>"}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("deploy_files: %v %+v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"url":"https://`) {
		t.Fatalf("deploy result: %s", b)
	}
	_ = cs.Close()
	// The OAuth token does not open the REST API.
	if r, _ := f.do(http.MethodGet, "/api/v1/me", "", nil, "Authorization", "Bearer "+tk.AccessToken); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("oauth token on API: %d", r.StatusCode)
	}

	// Refresh rotation and reuse detection.
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.RefreshToken}, "client_id": {clientID}}
	status, tk2 := f.token(refresh)
	if status != http.StatusOK || tk2.AccessToken == "" || tk2.RefreshToken == tk.RefreshToken {
		t.Fatalf("refresh: %d %+v", status, tk2)
	}
	if f.mcpStatus(tk2.AccessToken) != http.StatusOK {
		t.Fatal("rotated access token rejected")
	}
	if status, again := f.token(refresh); status != http.StatusBadRequest || again.Error != "invalid_grant" {
		t.Fatalf("refresh reuse: %d %+v", status, again)
	}
	if f.mcpStatus(tk2.AccessToken) != http.StatusUnauthorized {
		t.Fatal("chain not revoked after refresh reuse")
	}

	// A new connection, disconnected in the interface.
	loc, _ = url.Parse(f.authorize(clientID, f.connectCode(alice), "allow").Header.Get("Location"))
	tk3 := f.exchange(clientID, loc)
	if f.mcpStatus(tk3.AccessToken) != http.StatusOK {
		t.Fatal("new access token rejected")
	}
	grants, err := f.a.svc.ListOAuthGrants(context.Background(), alice)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants: %v %v", grants, err)
	}
	if err := f.a.svc.RevokeOAuthGrant(context.Background(), alice, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if f.mcpStatus(tk3.AccessToken) != http.StatusUnauthorized {
		t.Fatal("disconnected application still works")
	}
	if status, _ := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk3.RefreshToken}, "client_id": {clientID}}); status != http.StatusBadRequest {
		t.Fatal("disconnected refresh token works")
	}
}

func TestMCPWithAPITokenAndConsentErrors(t *testing.T) {
	f := newFlow(t)
	bob := f.user("bob@pp.test")
	nt, err := f.a.svc.CreateToken(context.Background(), bob, "mcp", 0)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := f.mcpSession(nt.Value)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_sites", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_sites: %v %+v", err, res)
	}
	_ = cs.Close()

	// Unknown client: an error page, never a redirect.
	resp, body := f.do(http.MethodGet, "/oauth/authorize?client_id=nope&redirect_uri=https://evil.example/cb", "", nil)
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" || !strings.Contains(body, "Подключение не удалось") {
		t.Fatalf("unknown client: %d %v", resp.StatusCode, resp.Header)
	}
	clientID := f.register()
	// Redirect URI mismatch: error page.
	resp, _ = f.do(http.MethodGet, "/oauth/authorize?response_type=code&client_id="+clientID+"&redirect_uri=https://evil.example/cb", "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("redirect mismatch: %d", resp.StatusCode)
	}
	// Missing PKCE: redirect with an error.
	resp, _ = f.do(http.MethodGet, "/oauth/authorize?response_type=code&state=s&client_id="+clientID+"&redirect_uri="+url.QueryEscape(redirectURI), "", nil)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("missing pkce: %d %s", resp.StatusCode, loc)
	}
	// Forged form.
	resp, _ = f.form("/oauth/authorize", url.Values{"_csrf": {"x"}, "request": {"y"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged form: %d", resp.StatusCode)
	}
	// Static assets of the consent page.
	if r, _ := f.do(http.MethodGet, "/oauth/static/app.css", "", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("static: %d", r.StatusCode)
	}
}
