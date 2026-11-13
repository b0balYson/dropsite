//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestCollectionTemplates publishes a page with the menu template's snippet,
// creates the collection from the template with samples and reads the data
// through Caddy, as a page of the site does.
func TestCollectionTemplates(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin-datatpl")}
	ann := createUser(t, s, admin, "datatpl@dropsite.localhost")

	st, body := ann.json(http.MethodGet, "/collection-templates", nil)
	must(t, st, http.StatusOK, body, "list templates")
	var snippet string
	for _, it := range body["items"].([]any) {
		tpl := it.(map[string]any)
		if tpl["name"] == "menu" {
			snippet = tpl["snippet"].(string)
		}
	}
	if !strings.Contains(snippet, "const NAME = 'menu'") {
		t.Fatalf("menu snippet: %.200q", snippet)
	}

	st, body = ann.json(http.MethodPost, "/sites", map[string]any{"title": "Bistro"})
	must(t, st, http.StatusCreated, body, "create site")
	id := str(body, "id")
	page := "<!doctype html><html lang=\"en\"><body><h1>Bistro</h1>" + snippet + "</body></html>"
	st, body = ann.deploy(id, makeZip(t, site(map[string]string{"index.html": page})...))
	must(t, st, http.StatusCreated, body, "deploy")
	host := id + "." + baseDomain
	s.expectBody(host, "/", 200, "<!-- Fallback content")

	st, body = ann.json(http.MethodPost, "/sites/"+id+"/collections", map[string]any{"name": "menu", "template": "menu",
		"with_samples": true})
	must(t, st, http.StatusCreated, body, "create from template")
	if body["mode"] != "public" || body["documents"] != float64(4) {
		t.Fatalf("collection %v", body)
	}
	resp, b, err := s.get(host, "/_dropsite/data/menu?limit=500") //nolint:bodyclose // s.get closes it
	if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(b, "Cappuccino") || !strings.Contains(b, `"next":null`) {
		t.Fatalf("read menu: %v %v %.200q", statusOf(resp), err, b)
	}
}
