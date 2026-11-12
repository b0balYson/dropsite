//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestAssistantAPI: the API endpoints behind the local MCP server of the CLI
// (dropsite mcp) work through Caddy: a site is edited relative to the
// release that was read, a stale edit is refused, server-side fetch never
// reaches private addresses, and connections and the root domain behave
// like the chat tools.
func TestAssistantAPI(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-assistant")}

	st, body := owner.json(http.MethodPost, "/uploads", map[string]string{"title": "Bakery"})
	must(t, st, http.StatusCreated, body, "begin upload")
	id := str(body, "id")
	st, body = owner.json(http.MethodPost, "/uploads/"+id+"/files", map[string]any{"files": []map[string]string{
		{"path": "index.html", "content": "<p>Bread 50</p>"}, {"path": "a.css", "content": "p{color:tan}"}}})
	must(t, st, http.StatusOK, body, "upload files")
	st, body = owner.json(http.MethodPost, "/uploads/"+id+"/commit", nil)
	must(t, st, http.StatusCreated, body, "commit upload")
	host := hostOf(str(body, "url"))
	siteID := str(body["site"].(map[string]any), "id")
	s.expectBody(host, "/", http.StatusOK, "Bread 50")

	st, body = owner.json(http.MethodGet, "/sites/"+siteID+"/files", nil)
	must(t, st, http.StatusOK, body, "list files")
	base := str(body, "deploy_id")
	st, body = owner.json(http.MethodPost, "/sites/"+siteID+"/files/read", map[string]any{"paths": []string{"index.html"}})
	must(t, st, http.StatusOK, body, "read files")
	if !strings.Contains(body["files"].([]any)[0].(map[string]any)["content"].(string), "Bread 50") {
		t.Fatalf("read: %v", body)
	}
	edit := map[string]any{"base_deploy_id": base, "files": []map[string]string{{"path": "index.html", "content": "<p>Bread 55</p>"}}}
	st, body = owner.json(http.MethodPatch, "/sites/"+siteID+"/files", edit)
	must(t, st, http.StatusOK, body, "update files")
	s.expectBody(host, "/", http.StatusOK, "Bread 55")
	s.expectBody(host, "/a.css", http.StatusOK, "tan")
	if st, body = owner.json(http.MethodPatch, "/sites/"+siteID+"/files", edit); st != http.StatusConflict || errCode(body) != "deploy_conflict" {
		t.Fatalf("stale edit: %d %v", st, body)
	}
	st, body = owner.json(http.MethodPatch, "/sites/"+siteID+"/files", map[string]any{"base_deploy_id": str(body["error"].(map[string]any)["details"].(map[string]any), "current"),
		"fetch": []map[string]string{{"url": "https://127.0.0.1/x.png", "path": "x.png"}}})
	if errCode(body) != "fetch_failed" {
		t.Fatalf("private fetch: %d %v", st, body)
	}

	st, body = owner.json(http.MethodGet, "/connections", nil)
	must(t, st, http.StatusOK, body, "connections")
	current := 0
	for _, c := range body["items"].([]any) {
		if c.(map[string]any)["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current connection: %v", body)
	}
	if st, body = owner.json(http.MethodPut, "/root-site", map[string]string{"site": siteID, "confirm": "nope"}); st != http.StatusBadRequest {
		t.Fatalf("root without confirm: %d %v", st, body)
	}
}
