//go:build e2e

package e2e

import (
	"net/http"
	"testing"
)

// TestReferences: the reference endpoint is reachable through Caddy, needs
// a token and refuses plain http and private addresses before fetching
// anything (the stand has no reliable internet, so no page is fetched).
func TestReferences(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-references")}

	for _, u := range []string{"http://example.com/", "https://127.0.0.1/", "https://10.1.2.3/", "https://localhost/"} {
		st, body := owner.json(http.MethodPost, "/references/inspect", map[string]string{"url": u})
		if st != http.StatusBadRequest || errCode(body) != "invalid_input" {
			t.Fatalf("%s: %d %v", u, st, body)
		}
	}
	anon := &api{s: s}
	st, body := anon.json(http.MethodPost, "/references/inspect", map[string]string{"url": "https://example.com/"})
	if st != http.StatusUnauthorized {
		t.Fatalf("without token: %d %v", st, body)
	}
}
