//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// mcpTool calls one MCP tool and returns its structured result, or the error
// text when the tool failed.
func (s *stack) mcpTool(token, name string, args map[string]any) (map[string]any, string) {
	s.t.Helper()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	resp, body := s.raw(s.client, http.MethodPost, "/mcp", "application/json", string(req),
		"Authorization", "Bearer "+token, "Accept", "application/json, text/event-stream")
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s: HTTP %d %.300s", name, resp.StatusCode, body)
	}
	var out struct {
		Result struct {
			IsError           bool             `json:"isError"`
			Content           []map[string]any `json:"content"`
			StructuredContent map[string]any   `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		s.t.Fatalf("%s: %v %.300s", name, err, body)
	}
	if out.Result.IsError {
		text, _ := out.Result.Content[0]["text"].(string)
		return nil, text
	}
	return out.Result.StructuredContent, ""
}

// TestChatCabinet manages a site from a chat assistant through Caddy: access
// settings, statistics, own domains, and the root domain by the
// administrator's assistant.
func TestChatCabinet(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	token := admin.issueToken("e2e-cabinet")

	out, msg := s.mcpTool(token, "deploy_files", map[string]any{"title": "Soon",
		"files": []map[string]string{{"path": "index.html", "content": "<h1>coming soon</h1>"}}})
	if msg != "" {
		t.Fatal(msg)
	}
	siteID := str(out, "site_id")
	host := siteID + "." + baseDomain
	s.expectBody(host, "/", 200, "coming soon")

	if out, msg = s.mcpTool(token, "update_site", map[string]any{"site": siteID, "title": "Coming soon", "slug": "e2e-soon"}); msg != "" ||
		!strings.Contains(str(out, "url"), "e2e-soon.") {
		t.Fatalf("update_site: %v %q", out, msg)
	}
	s.expectBody("e2e-soon."+baseDomain, "/", 200, "coming soon")

	if _, msg = s.mcpTool(token, "set_site_access", map[string]any{"site": siteID, "password": "e2e-visitor-pass"}); msg != "" {
		t.Fatal(msg)
	}
	s.eventually("password page", func() error {
		_, body, err := s.get(host, "/")
		if err == nil && strings.Contains(body, "coming soon") {
			return errors.New("site still open")
		}
		return err
	})
	if _, msg = s.mcpTool(token, "set_site_access", map[string]any{"site": siteID, "remove_password": true}); msg != "" {
		t.Fatal(msg)
	}
	s.expectBody(host, "/", 200, "coming soon")

	if out, msg = s.mcpTool(token, "get_site_stats", map[string]any{"site": siteID}); msg != "" || len(out["daily"].([]any)) != 7 {
		t.Fatalf("stats: %v %q", out, msg)
	}
	if out, msg = s.mcpTool(token, "add_domain", map[string]any{"site": siteID, "domain": "soon.example.com"}); msg != "" ||
		!strings.Contains(str(out, "summary"), "administrator") {
		t.Fatalf("add_domain: %v %q", out, msg)
	}
	if _, msg = s.mcpTool(token, "remove_domain", map[string]any{"site": siteID, "domain": "soon.example.com",
		"confirm": "soon.example.com"}); msg != "" {
		t.Fatal(msg)
	}

	// The administrator's assistant puts the placeholder on the root domain.
	if _, msg = s.mcpTool(token, "set_root_site", map[string]any{"site": siteID, "confirm": "wrong"}); !strings.Contains(msg, "confirm") {
		t.Fatalf("unconfirmed root: %q", msg)
	}
	if _, msg = s.mcpTool(token, "set_root_site", map[string]any{"site": siteID, "confirm": "e2e-soon"}); msg != "" {
		t.Fatal(msg)
	}
	s.expectBody(baseDomain, "/", 200, "coming soon")
	s.expectBody(host, "/", 200, "coming soon")
	if _, msg = s.mcpTool(token, "clear_root_site", map[string]any{"confirm": siteID}); msg != "" {
		t.Fatal(msg)
	}
	s.expectBody(host, "/", 200, "coming soon")

	// A regular user's assistant is refused.
	user := createUser(t, s, &api{s: s, token: token}, "e2e-cabinet-user@dropsite.localhost")
	if _, msg = s.mcpTool(user.token, "set_root_site", map[string]any{"site": siteID, "confirm": siteID}); !strings.Contains(msg, "only an administrator") {
		t.Fatalf("regular user root: %q", msg)
	}
	if _, msg = s.mcpTool(user.token, "get_site", map[string]any{"site": siteID}); !strings.Contains(msg, "not found") {
		t.Fatalf("foreign site: %q", msg)
	}
}
