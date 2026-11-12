//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestSiteCollections reads collection data through Caddy on an open site,
// checks that a protected site does not leak it before sign-in, and walks a
// visitor proposal through moderation.
func TestSiteCollections(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin-data")}
	ann := createUser(t, s, admin, "data@dropsite.localhost")

	newSite := func(title string) (string, string) {
		t.Helper()
		st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": title})
		must(t, st, http.StatusCreated, body, "create "+title)
		id := str(body, "id")
		st, body = ann.deploy(id, makeZip(t, site(map[string]string{"index.html": "<h1>" + title + "</h1>"})...))
		must(t, st, http.StatusCreated, body, "deploy "+title)
		host := id + "." + baseDomain
		s.expectBody(host, "/", 200, title)
		return id, host
	}
	openID, openHost := newSite("Cafe")
	lockedID, lockedHost := newSite("Private")

	coll := func(siteID string) string { return "/sites/" + siteID + "/collections" }
	for _, id := range []string{openID, lockedID} {
		st, body := ann.json(http.MethodPost, coll(id), map[string]any{"name": "menu", "mode": "public"})
		must(t, st, http.StatusCreated, body, "create menu")
		st, body = ann.json(http.MethodPost, coll(id)+"/menu/documents", map[string]any{"documents": []map[string]any{
			{"id": "cappuccino", "data": map[string]any{"name": "Cappuccino", "price": 250}},
		}})
		must(t, st, http.StatusOK, body, "write menu")
	}

	t.Run("open site serves its data", func(t *testing.T) {
		resp, b, err := s.get(openHost, "/_dropsite/data/menu") //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(b, "Cappuccino") ||
			resp.Header.Get("Cache-Control") != "no-cache" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("read menu: %v %v %.200q", statusOf(resp), err, b)
		}
		resp, b, err = s.get(openHost, "/_dropsite/data/nope") //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusNotFound || !strings.Contains(b, `"not_found"`) {
			t.Fatalf("unknown collection: %v %v %.200q", statusOf(resp), err, b)
		}
		// Other /_dropsite paths are still served by the platform as before.
		if r, _, err := s.get(openHost, "/_dropsite/altcha.js"); err != nil || r.StatusCode != http.StatusOK { //nolint:bodyclose // s.get closes it
			t.Fatalf("altcha widget: %v %v", statusOf(r), err)
		}
	})

	t.Run("protected site does not leak data", func(t *testing.T) {
		st, body := ann.json(http.MethodPatch, "/sites/"+lockedID, map[string]any{"password": "visitor-pass-1"})
		must(t, st, http.StatusOK, body, "protect")
		resp, b, err := s.get(lockedHost, "/_dropsite/data/menu") //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusFound || strings.Contains(b, "Cappuccino") {
			t.Fatalf("data without access: %v %v %.200q", statusOf(resp), err, b)
		}
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/_dropsite/login?next=") {
			t.Fatalf("location %q", loc)
		}
		cookie := visitorLogin(t, s, lockedHost, "visitor-pass-1")
		resp, b, err = s.get(lockedHost, "/_dropsite/data/menu", "Cookie", cookie) //nolint:bodyclose // s.get closes it
		if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(b, "Cappuccino") {
			t.Fatalf("data with access: %v %v %.200q", statusOf(resp), err, b)
		}
	})

	t.Run("visitor proposal and moderation", func(t *testing.T) {
		schema := map[string]any{"type": "object", "required": []string{"name", "text"}, "properties": map[string]any{
			"name": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"},
		}}
		st, body := ann.json(http.MethodPost, coll(openID), map[string]any{"name": "reviews", "mode": "moderated", "schema": schema})
		must(t, st, http.StatusCreated, body, "create reviews")

		post := func(path, payload, origin string) (int, string) {
			req, _ := http.NewRequestWithContext(s.ctx, http.MethodPost, "https://"+openHost+":"+httpsPort+path,
				strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			resp, err := s.client.Do(req)
			if err != nil {
				t.Fatalf("post %s: %v", path, err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		origin := "https://" + openHost + ":" + httpsPort
		if st, b := post("/_dropsite/data/reviews", `{"name":"Anna","text":"Tasty"}`, origin); st != http.StatusAccepted {
			t.Fatalf("proposal: %d %s", st, b)
		}
		if st, b := post("/_dropsite/data/reviews", `{"name":"Eve","text":"x","is_admin":true}`, origin); st != http.StatusBadRequest {
			t.Fatalf("extra field: %d %s", st, b)
		}
		if st, b := post("/_dropsite/data/reviews", `{"name":"Eve","text":"x"}`, "https://evil.example"); st != http.StatusForbidden {
			t.Fatalf("foreign origin: %d %s", st, b)
		}
		if st, b := post("/_dropsite/data/menu", `{"name":"x"}`, origin); st != http.StatusMethodNotAllowed {
			t.Fatalf("public collection: %d %s", st, b)
		}
		if st, b := post("/_dropsite/data/reviews", `{"name":"A","text":"`+strings.Repeat("x", 9<<10)+`"}`, origin); st != http.StatusRequestEntityTooLarge {
			t.Fatalf("large proposal: %d %.200s", st, b)
		}
		if _, b, _ := s.get(openHost, "/_dropsite/data/reviews"); strings.Contains(b, "Anna") { //nolint:bodyclose // s.get closes it
			t.Fatalf("pending review is public: %s", b)
		}
		st, body = ann.json(http.MethodGet, coll(openID)+"/reviews/documents?status=pending", nil)
		must(t, st, http.StatusOK, body, "pending")
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("pending %v", body)
		}
		id := str(items[0].(map[string]any), "id")
		st, body = ann.json(http.MethodPost, coll(openID)+"/reviews/moderate", map[string]any{"ids": []string{id}, "action": "approve"})
		must(t, st, http.StatusOK, body, "approve")
		if _, b, _ := s.get(openHost, "/_dropsite/data/reviews"); !strings.Contains(b, "Anna") { //nolint:bodyclose // s.get closes it
			t.Fatalf("approved review is not public: %s", b)
		}
	})
}
