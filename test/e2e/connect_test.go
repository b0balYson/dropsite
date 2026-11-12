//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var (
	connectCodeRe = regexp.MustCompile(`DROPSITE_CODE=([0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{5}) sh`)
	sealedRe      = regexp.MustCompile(`name="request" value="([^"]+)"`)
)

const apiBase = "https://" + apiHost + ":" + httpsPort

// connectCode presses "Connect" on the Connect AI page and returns the code.
func (u *ui) connectCode() string {
	u.t.Helper()
	tok := u.csrf("/connect")
	_, body := u.post("/connect/code", url.Values{"_csrf": {tok}})
	m := connectCodeRe.FindStringSubmatch(body)
	if m == nil {
		u.t.Fatalf("no connect code on the page:\n%.500s", body)
	}
	return m[1]
}

func (s *stack) raw(c *http.Client, method, path, ctype, body string, hdr ...string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequestWithContext(s.ctx, method, apiBase+path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (s *stack) mcpCall(token, method string) (int, string) {
	s.t.Helper()
	resp, body := s.raw(s.client, http.MethodPost, "/mcp", "application/json",
		`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":{}}`,
		"Authorization", "Bearer "+token, "Accept", "application/json, text/event-stream")
	return resp.StatusCode, body
}

// TestConnectAI walks the "Connect AI" flows through Caddy: a connect code
// exchanged by the CLI, MCP with an API token, and a chat client connecting
// over OAuth with a connect code instead of a password.
func TestConnectAI(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)

	t.Run("connect code becomes an API token", func(t *testing.T) {
		code := admin.connectCode()
		st, body := (&api{s: s}).json(http.MethodPost, "/connect/exchange", map[string]any{"code": code})
		must(t, st, http.StatusCreated, body, "exchange")
		token := str(body, "value")
		st, body = (&api{s: s}).json(http.MethodPost, "/connect/exchange", map[string]any{"code": code})
		if st != http.StatusBadRequest || errCode(body) != "connect_code_invalid" {
			t.Fatalf("second exchange: %d %v", st, body)
		}
		if st, out := s.mcpCall(token, "tools/list"); st != http.StatusOK || !strings.Contains(out, "deploy_files") {
			t.Fatalf("mcp tools/list: %d %.300s", st, out)
		}
	})

	t.Run("chat client over OAuth", func(t *testing.T) {
		resp, _ := s.raw(s.client, http.MethodPost, "/mcp", "application/json", `{}`)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
			t.Fatalf("challenge: %d %v", resp.StatusCode, resp.Header)
		}
		_, meta := s.raw(s.client, http.MethodGet, "/.well-known/oauth-authorization-server", "", "")
		if !strings.Contains(meta, `"code_challenge_methods_supported":["S256"]`) {
			t.Fatalf("metadata: %s", meta)
		}
		_, reg := s.raw(s.client, http.MethodPost, "/oauth/register", "application/json",
			`{"redirect_uris":["https://chat.example/cb"],"client_name":"E2E chat"}`)
		var client struct {
			ClientID string `json:"client_id"`
		}
		_ = json.Unmarshal([]byte(reg), &client)

		verifier := strings.Repeat("e2e-verifier-", 4)
		sum := sha256.Sum256([]byte(verifier))
		q := url.Values{"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {"https://chat.example/cb"},
			"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
			"state": {"e2e"}, "resource": {apiBase + "/mcp"}}
		browser := *s.client
		browser.Jar, _ = cookiejar.New(nil)
		_, page := s.raw(&browser, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
		csrf, sealed := csrfRe.FindStringSubmatch(page), sealedRe.FindStringSubmatch(page)
		if csrf == nil || sealed == nil || strings.Contains(page, `type="password"`) {
			t.Fatalf("consent page:\n%.800s", page)
		}
		resp, _ = s.raw(&browser, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", url.Values{
			"_csrf": {csrf[1]}, "request": {strings.ReplaceAll(sealed[1], "&#43;", "+")}, "code": {admin.connectCode()}, "action": {"allow"},
		}.Encode())
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusSeeOther || loc.Query().Get("code") == "" {
			t.Fatalf("consent: %d %s", resp.StatusCode, loc)
		}
		_, tok := s.raw(s.client, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", url.Values{
			"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier},
			"client_id": {client.ClientID}, "redirect_uri": {"https://chat.example/cb"}, "resource": {apiBase + "/mcp"},
		}.Encode())
		var tokens struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal([]byte(tok), &tokens)
		if st, out := s.mcpCall(tokens.AccessToken, "tools/list"); st != http.StatusOK || !strings.Contains(out, "list_sites") {
			t.Fatalf("mcp with oauth token: %d %.300s", st, out)
		}
		connect := admin.csrf("/connect")
		if _, page := admin.post("/connect/code", url.Values{"_csrf": {connect}}); !strings.Contains(page, "E2E chat") {
			t.Fatal("connected application is not listed")
		}
	})
}
