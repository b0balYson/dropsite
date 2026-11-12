//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestWildcardPreviews checks release previews on a wildcard stand. It is
// skipped unless the stack runs in wildcard mode:
//
//	E2E_WILDCARD=1 go test -count=1 -tags e2e -run TestWildcardPreviews ./test/e2e/
func TestWildcardPreviews(t *testing.T) {
	if !wildcardStand() {
		t.Skip("set E2E_WILDCARD=1 to run against a wildcard TLS stand")
	}
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}
	ann := createUser(t, s, admin, "ann@dropsite.localhost")

	st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": "Preview"})
	must(t, st, http.StatusCreated, body, "create site")
	siteID := str(body, "id")
	siteHost := hostOf(str(body, "url"))

	var previews []string
	for _, v := range []string{"v1", "v2"} {
		st, body = ann.deploy(siteID, makeZip(t, site(map[string]string{"dist/index.html": "<h1>" + v + "</h1>"})...))
		must(t, st, http.StatusCreated, body, "deploy "+v)
		deploy, _ := body["deploy"].(map[string]any)
		url, _ := deploy["preview_url"].(string)
		want := fmt.Sprintf("https://p-%s-%s.%s:%s/", str(body, "deploy_id"), siteID, baseDomain, httpsPort)
		if url != want {
			t.Fatalf("preview_url %q, want %q", url, want)
		}
		previews = append(previews, hostOf(url))
		time.Sleep(1100 * time.Millisecond) // distinct deploy IDs
	}

	s.expectBody(siteHost, "/", 200, "<h1>v2</h1>")
	s.expectBody(previews[0], "/", 200, "<h1>v1</h1>")
	s.expectBody(previews[1], "/", 200, "<h1>v2</h1>")

	t.Run("previews are not indexed, sites are", func(t *testing.T) {
		resp, _, err := s.get(previews[0], "/")
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Header.Get("X-Robots-Tag"); got != "noindex" {
			t.Fatalf("preview X-Robots-Tag %q", got)
		}
		resp, _, err = s.get(siteHost, "/")
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Header.Get("X-Robots-Tag"); got != "" {
			t.Fatalf("site X-Robots-Tag %q", got)
		}
	})

	t.Run("site and preview share the wildcard certificate", func(t *testing.T) {
		for _, h := range []string{siteHost, previews[0]} {
			resp, _, err := s.get(h, "/")
			if err != nil {
				t.Fatal(err)
			}
			cert := resp.TLS.PeerCertificates[0]
			if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "*."+baseDomain {
				t.Fatalf("%s served certificate for %v", h, cert.DNSNames)
			}
		}
	})

	t.Run("deleting the site removes its previews", func(t *testing.T) {
		st, body := ann.json(http.MethodDelete, "/sites/"+siteID, nil)
		if st != http.StatusNoContent && st != http.StatusOK {
			t.Fatalf("delete: %d %v", st, body)
		}
		for _, h := range previews {
			s.expectBody(h, "/", 404, "")
		}
	})
}
