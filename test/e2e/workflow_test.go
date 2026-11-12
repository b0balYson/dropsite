//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestWorkflow: a site from a template and its copy are served through
// Caddy; a prepared release scheduled a few seconds ahead goes live without
// a restart; without a renderer screenshots answer 503 renderer_unavailable.
func TestWorkflow(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-workflow")}

	st, body := owner.json(http.MethodPost, "/templates/cafe/sites", map[string]string{"title": "E2E Coffee", "lang": "en"})
	must(t, st, http.StatusCreated, body, "site from template")
	siteID := str(body["site"].(map[string]any), "id")
	s.expectBody(hostOf(str(body, "url")), "/", http.StatusOK, "E2E Coffee")

	st, body = owner.json(http.MethodPost, "/sites/"+siteID+"/clone", map[string]string{"title": "E2E Coffee 2"})
	must(t, st, http.StatusCreated, body, "clone")
	// A copy serves the same files: the new title names the site, it does not
	// rewrite its pages.
	s.expectBody(hostOf(str(body, "url")), "/", http.StatusOK, "E2E Coffee")

	st, body = owner.json(http.MethodPost, "/uploads", map[string]string{"site": siteID})
	must(t, st, http.StatusCreated, body, "begin upload")
	id := str(body, "id")
	st, body = owner.json(http.MethodPost, "/uploads/"+id+"/files", map[string]any{"files": []map[string]string{
		{"path": "index.html", "content": "<p>Scheduled sale</p>"}}})
	must(t, st, http.StatusOK, body, "upload files")
	at := time.Now().Add(20 * time.Second).UTC().Format(time.RFC3339)
	st, body = owner.json(http.MethodPost, "/uploads/"+id+"/schedule", map[string]string{"publish_at": at})
	must(t, st, http.StatusCreated, body, "schedule upload")
	host := hostOf(str(body, "site_url"))
	s.expectBody(host, "/", http.StatusOK, "E2E Coffee")
	deadline := time.Now().Add(90 * time.Second)
	for {
		st, body = owner.json(http.MethodGet, "/schedules?site="+siteID, nil)
		must(t, st, http.StatusOK, body, "list schedules")
		if str(body["items"].([]any)[0].(map[string]any), "status") == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not published: %v", body)
		}
		time.Sleep(2 * time.Second)
	}
	s.expectBody(host, "/", http.StatusOK, "Scheduled sale")

	if rendererStand() {
		return // TestScreenshots covers the renderer
	}
	st, body = owner.json(http.MethodGet, "/sites/"+siteID+"/visual-diff?from=x", nil)
	if st != http.StatusServiceUnavailable || errCode(body) != "renderer_unavailable" {
		t.Fatalf("visual diff without renderer: %d %v", st, body)
	}
}
