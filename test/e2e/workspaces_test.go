//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestWorkspaces: a member publishes a site another member created in a
// shared workspace, a stranger gets 404, and moving the site out of the
// workspace keeps it served through Caddy.
func TestWorkspaces(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-workspaces")}
	stamp := time.Now().UnixNano()
	annEmail, bobEmail := fmt.Sprintf("ws-ann-%d@example.test", stamp), fmt.Sprintf("ws-bob-%d@example.test", stamp)
	ann := createUser(t, s, admin, annEmail)
	bob := createUser(t, s, admin, bobEmail)
	eve := createUser(t, s, admin, fmt.Sprintf("ws-eve-%d@example.test", stamp))

	st, body := ann.json(http.MethodPost, "/workspaces", map[string]string{"name": "E2E team"})
	must(t, st, http.StatusCreated, body, "create workspace")
	ws := str(body, "id")
	st, body = ann.json(http.MethodPost, "/workspaces/"+ws+"/members", map[string]string{"email": bobEmail})
	must(t, st, http.StatusCreated, body, "add member")

	st, body = ann.json(http.MethodPost, "/sites", map[string]string{"title": "E2E shared", "workspace_id": ws})
	must(t, st, http.StatusCreated, body, "create site in workspace")
	siteID := str(body, "id")
	st, body = bob.deploy(siteID, makeZip(t, site(map[string]string{"index.html": "<p>Published by Bob</p>"})...))
	must(t, st, http.StatusCreated, body, "member deploy")
	host := hostOf(str(body, "url"))
	s.expectBody(host, "/", http.StatusOK, "Published by Bob")

	st, body = eve.json(http.MethodGet, "/sites/"+siteID, nil)
	must(t, st, http.StatusNotFound, body, "stranger")

	st, body = ann.json(http.MethodPost, "/sites/"+siteID+"/move", map[string]any{"workspace_id": nil})
	must(t, st, http.StatusOK, body, "move out")
	st, body = bob.json(http.MethodGet, "/sites/"+siteID, nil)
	must(t, st, http.StatusNotFound, body, "former member view")
	s.expectBody(host, "/", http.StatusOK, "Published by Bob")
}
