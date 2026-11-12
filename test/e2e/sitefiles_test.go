//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// mcpTool calls one MCP tool through Caddy and returns its structured
// result, or the error text when the tool failed.
func (s *stack) mcpTool(token, name string, args map[string]any) (map[string]any, string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	resp, out := s.raw(s.client, http.MethodPost, "/mcp", "application/json", string(body),
		"Authorization", "Bearer "+token, "Accept", "application/json, text/event-stream")
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s: HTTP %d %.300s", name, resp.StatusCode, out)
	}
	var rpc struct {
		Result struct {
			IsError           bool             `json:"isError"`
			Content           []map[string]any `json:"content"`
			StructuredContent map[string]any   `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &rpc); err != nil {
		s.t.Fatalf("%s: %v %.300s", name, err, out)
	}
	if rpc.Result.IsError {
		text, _ := rpc.Result.Content[0]["text"].(string)
		return nil, text
	}
	return rpc.Result.StructuredContent, ""
}

// TestChatEditsSiteFiles: an assistant publishes a site, reads it back in a
// "new chat", changes one file relative to the release it read, and a stale
// second edit is refused.
func TestChatEditsSiteFiles(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	token := admin.issueToken("e2e-files")

	out, msg := s.mcpTool(token, "deploy_files", map[string]any{"title": "Cafe", "files": []map[string]string{
		{"path": "index.html", "content": "<p>Coffee 100</p>"}, {"path": "menu.css", "content": "p{color:brown}"},
	}})
	if msg != "" {
		t.Fatal(msg)
	}
	site, host := str(out, "site_id"), hostOf(str(out, "url"))
	s.expectBody(host, "/", http.StatusOK, "Coffee 100")

	out, msg = s.mcpTool(token, "list_site_files", map[string]any{"site": site})
	if msg != "" || out["total_files"].(float64) != 2 {
		t.Fatalf("list: %v %q", out, msg)
	}
	base := str(out, "deploy_id")
	out, msg = s.mcpTool(token, "read_site_files", map[string]any{"site": site, "paths": []string{"index.html"}})
	if msg != "" || !strings.Contains(out["files"].([]any)[0].(map[string]any)["content"].(string), "Coffee 100") {
		t.Fatalf("read: %v %q", out, msg)
	}
	out, msg = s.mcpTool(token, "update_site_files", map[string]any{"site": site, "base_deploy_id": base,
		"files": []map[string]string{{"path": "index.html", "content": "<p>Coffee 120</p>"}}})
	if msg != "" {
		t.Fatal(msg)
	}
	s.expectBody(host, "/", http.StatusOK, "Coffee 120")
	s.expectBody(host, "/menu.css", http.StatusOK, "brown")
	if _, msg = s.mcpTool(token, "update_site_files", map[string]any{"site": site, "base_deploy_id": base,
		"files": []map[string]string{{"path": "index.html", "content": "stale"}}}); !strings.Contains(msg, "deploy_conflict") {
		t.Fatalf("stale edit: %q", msg)
	}
	// Server-side fetch never reaches the control host itself.
	if _, msg = s.mcpTool(token, "update_site_files", map[string]any{"site": site, "base_deploy_id": str(out, "deploy_id"),
		"fetch": []map[string]string{{"url": "https://127.0.0.1/x.png", "path": "x.png"}}}); !strings.Contains(msg, "fetch_failed") {
		t.Fatalf("private fetch: %q", msg)
	}
}
