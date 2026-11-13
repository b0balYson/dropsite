//go:build e2e

package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

// Client addresses for the abuse-protection checks. compose.forms.yml makes
// Caddy trust X-Test-Client-Ip from the test client (a private address), so
// a request can come "from" an AI crawler or a cloud.
const (
	clientIPHeader = "X-Test-Client-Ip"
	openAIAddr     = "135.234.64.10" // openai range of caddy-defender
	awsAddr        = "3.5.140.10"    // aws range
	bannedAddr     = "203.0.113.77"
)

func controlCmd(t *testing.T, s *stack, args ...string) string {
	t.Helper()
	return run(t, s.ctx, append([]string{"exec", "-T", "control", "/control"}, args...)...)
}

func (s *stack) request(method, host, path string, body []byte, header ...string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequestWithContext(s.ctx, method, "https://"+host+":"+httpsPort+path, bytes.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestAbuseProtection walks through the visitor session of an ai endpoint,
// the defender switch (a file marker, no Caddy reload) and a ban marker.
func TestAbuseProtection(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}
	ann := createUser(t, s, admin, "abuse@dropsite.localhost")
	st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": "Guard"})
	must(t, st, http.StatusCreated, body, "create site")
	id := str(body, "id")
	st, body = ann.deploy(id, makeZip(t, site(map[string]string{"index.html": "<h1>Guard</h1>"})...))
	must(t, st, http.StatusCreated, body, "deploy")
	host := id + "." + baseDomain
	s.expectBody(host, "/", 200, "Guard")
	origin := "https://" + host + ":" + httpsPort

	t.Run("session", func(t *testing.T) {
		cfg := map[string]any{"upstream_url": "https://api.openai.com/v1", "auth": "bearer",
			"template": map[string]any{"path": "/chat/completions",
				"body":     map[string]any{"model": "gpt-4o-mini", "messages": []any{map[string]any{"role": "user", "content": "{{q}}"}}},
				"fields":   map[string]any{"q": map[string]any{"type": "string", "max_length": 100}},
				"response": "choices.0.message.content"}}
		st, body := ann.json(http.MethodPut, "/sites/"+id+"/proxy/endpoints/chat", cfg)
		must(t, st, http.StatusCreated, body, "create ai endpoint")
		if body["class"] != "ai" {
			t.Fatalf("class: %v", body)
		}
		resp, js := s.request(http.MethodGet, host, "/_dropsite/session.js", nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(js, "X-Dropsite-Session") {
			t.Fatalf("session.js: %d %.80s", resp.StatusCode, js)
		}
		call := func(token string) (*http.Response, string) {
			return s.request(http.MethodPost, host, "/_dropsite/proxy/chat", []byte(`{"q":"hi"}`),
				"Content-Type", "application/json", "Origin", origin, "X-Dropsite-Session", token)
		}
		if resp, b := call(""); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(b, "proxy_session") {
			t.Fatalf("without a session: %d %s", resp.StatusCode, b)
		}
		resp, chBody := s.request(http.MethodGet, host, "/_dropsite/session/challenge", nil)
		var ch altcha.Challenge
		if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(chBody), &ch) != nil {
			t.Fatalf("challenge: %d %s", resp.StatusCode, chBody)
		}
		sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeySHA(), Timeout: time.Minute})
		if err != nil || sol == nil {
			t.Fatalf("solve: %v", err)
		}
		pay, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
		req, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(pay)})
		resp, grant := s.request(http.MethodPost, host, "/_dropsite/session", req, "Content-Type", "application/json")
		var g struct {
			Token string `json:"token"`
		}
		if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(grant), &g) != nil || g.Token == "" {
			t.Fatalf("session: %d %s", resp.StatusCode, grant)
		}
		// The session is accepted; the endpoint still has no key.
		if resp, b := call(g.Token); resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(b, "proxy_secret_missing") {
			t.Fatalf("with a session: %d %s", resp.StatusCode, b)
		}
		// The token is bound to the client address.
		resp, b := s.request(http.MethodPost, host, "/_dropsite/proxy/chat", []byte(`{"q":"hi"}`),
			"Origin", origin, "X-Dropsite-Session", g.Token, clientIPHeader, "198.51.100.40")
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(b, "proxy_session") {
			t.Fatalf("token from another address: %d %s", resp.StatusCode, b)
		}
	})

	t.Run("defender", func(t *testing.T) {
		page := func(ip, path string) int {
			resp, _ := s.request(http.MethodGet, host, path, nil, clientIPHeader, ip)
			return resp.StatusCode
		}
		if got := controlCmd(t, s, "defender", "status"); got != "on\nnormal\n" {
			t.Fatalf("default status %q", got)
		}
		if c := page(openAIAddr, "/"); c != http.StatusForbidden {
			t.Fatalf("AI crawler on a page: %d", c)
		}
		if c := page(awsAddr, "/"); c != http.StatusOK {
			t.Fatalf("cloud on a page: %d", c)
		}
		if c := page(awsAddr, "/_dropsite/proxy/chat"); c != http.StatusForbidden {
			t.Fatalf("cloud on the proxy: %d", c)
		}
		controlCmd(t, s, "defender", "off")
		if c := page(openAIAddr, "/"); c != http.StatusOK {
			t.Fatalf("AI crawler with the defender off: %d", c)
		}
		controlCmd(t, s, "defender", "on")
		controlCmd(t, s, "defender", "strict", "on")
		if c := page(awsAddr, "/"); c != http.StatusForbidden {
			t.Fatalf("cloud on a page in strict mode: %d", c)
		}
		controlCmd(t, s, "defender", "strict", "off")
		// The API host is never blocked by range.
		resp, _ := s.request(http.MethodGet, apiHost, "/api/v1/openapi.json", nil, clientIPHeader, openAIAddr)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the API host from an AI range: %d", resp.StatusCode)
		}
	})

	t.Run("ban", func(t *testing.T) {
		controlCmd(t, s, "bans", "add", bannedAddr, "30")
		resp, _ := s.request(http.MethodGet, host, "/", nil, clientIPHeader, bannedAddr)
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("X-Dropsite-Block") != "ban" ||
			resp.Header.Get("Retry-After") == "" {
			t.Fatalf("banned client: %d %v", resp.StatusCode, resp.Header)
		}
		if resp, _ := s.request(http.MethodGet, host, "/", nil, clientIPHeader, "203.0.113.78"); resp.StatusCode != http.StatusOK {
			t.Fatalf("other client: %d", resp.StatusCode)
		}
		if out := controlCmd(t, s, "bans", "list"); !strings.Contains(out, bannedAddr) {
			t.Fatalf("bans list: %q", out)
		}
		controlCmd(t, s, "bans", "clear", bannedAddr)
		if resp, _ := s.request(http.MethodGet, host, "/", nil, clientIPHeader, bannedAddr); resp.StatusCode != http.StatusOK {
			t.Fatalf("after clear: %d", resp.StatusCode)
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		ip := "198.51.100.200"
		limited := 0
		for range 260 {
			resp, _ := s.request(http.MethodGet, host, "/", nil, clientIPHeader, ip)
			if resp.StatusCode == http.StatusTooManyRequests {
				if resp.Header.Get("X-Dropsite-Block") != "rate" {
					t.Fatalf("limited without the marker: %v", resp.Header)
				}
				limited++
			}
		}
		if limited == 0 {
			t.Fatal("no 429 after 260 requests")
		}
	})
}
