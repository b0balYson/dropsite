//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// mcpTool calls one MCP tool with an API token and returns the HTTP status
// and the raw JSON-RPC reply.
func (s *stack) mcpTool(token, name string, args map[string]any) (int, string) {
	s.t.Helper()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	resp, body := s.raw(s.client, http.MethodPost, "/mcp", "application/json", string(req),
		"Authorization", "Bearer "+token, "Accept", "application/json, text/event-stream")
	return resp.StatusCode, body
}

// mcpForms reads the forms of a site through the remote MCP tools: the
// delivered submission with its marker, a CSV export, the form snippet and
// the channels without secrets.
func mcpForms(t *testing.T, s *stack, owner *api, siteID, marker string) {
	st, out := s.mcpTool(owner.token, "list_submissions", map[string]any{"site": siteID, "query": marker})
	if st != http.StatusOK || !strings.Contains(out, marker) || !strings.Contains(out, "delivered at") ||
		strings.Contains(out, `"isError":true`) {
		t.Fatalf("mcp list_submissions: %d %.500s", st, out)
	}
	st, out = s.mcpTool(owner.token, "export_submissions", map[string]any{"site": siteID, "days": 1})
	if st != http.StatusOK || !strings.Contains(out, "created_at,form,host") || !strings.Contains(out, marker) {
		t.Fatalf("mcp export_submissions: %d %.500s", st, out)
	}
	st, out = s.mcpTool(owner.token, "get_form_settings", map[string]any{"site": siteID})
	if st != http.StatusOK || !strings.Contains(out, "/_form/") || !strings.Contains(out, "altcha-widget") {
		t.Fatalf("mcp get_form_settings: %d %.500s", st, out)
	}
	st, out = s.mcpTool(owner.token, "list_channels", map[string]any{"site": siteID})
	if st != http.StatusOK || !strings.Contains(out, "webhook-sink") || strings.Contains(out, "whsec_") {
		t.Fatalf("mcp list_channels: %d %.500s", st, out)
	}
}
