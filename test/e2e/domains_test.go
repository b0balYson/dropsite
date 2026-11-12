//go:build e2e

package e2e

import (
	"crypto/tls"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"
)

// externalHost resolves to 127.0.0.1 without DNS setup (*.localhost) and lies
// outside the base domain, so it is an external domain in both TLS modes.
const externalHost = "promo.external.localhost"

// handshake opens a TLS connection to Caddy with the given SNI and returns
// the names of the certificate it got.
func handshake(sni string) ([]string, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", "127.0.0.1:"+httpsPort,
		&tls.Config{ServerName: sni, InsecureSkipVerify: true}) //nolint:gosec // local CA in tests
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].DNSNames, nil
}

func TestExternalDomains(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}
	ann := createUser(t, s, admin, "ann@dropsite.localhost")
	bob := createUser(t, s, admin, "bob@dropsite.localhost")

	st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": "Promo"})
	must(t, st, http.StatusCreated, body, "create site")
	siteID := str(body, "id")
	siteHost := hostOf(str(body, "url"))
	st, body = ann.deploy(siteID, makeZip(t, site(map[string]string{"dist/index.html": "<h1>external</h1>"})...))
	must(t, st, http.StatusCreated, body, "deploy")
	s.expectBody(siteHost, "/", 200, "<h1>external</h1>")

	st, body = ann.json(http.MethodPost, "/sites/"+siteID+"/hostnames", map[string]any{"hostname": "Promo.External.Localhost"})
	must(t, st, http.StatusCreated, body, "add domain")
	if str(body, "hostname") != externalHost || str(body, "status") != "pending" {
		t.Fatalf("added %v", body)
	}

	t.Run("an unapproved domain gets no certificate", func(t *testing.T) {
		for range 3 {
			if names, err := handshake(externalHost); err == nil {
				t.Fatalf("handshake succeeded with certificate %v", names)
			}
			time.Sleep(500 * time.Millisecond)
		}
		// Nor does another site's certificate serve it.
		resp, body, err := s.get(siteHost, "/", "Host", externalHost)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusMisdirectedRequest || body == "<h1>external</h1>" {
			t.Fatalf("Host %s over %s: %d %.80q", externalHost, siteHost, resp.StatusCode, body)
		}
	})

	t.Run("another user cannot take the domain", func(t *testing.T) {
		st, body := bob.json(http.MethodPost, "/sites", map[string]any{"title": "Thief"})
		must(t, st, http.StatusCreated, body, "create site")
		st, body = bob.json(http.MethodPost, "/sites/"+str(body, "id")+"/hostnames", map[string]any{"hostname": externalHost})
		if st != http.StatusConflict || errCode(body) != "domain_taken" {
			t.Fatalf("taken domain: %d %v", st, body)
		}
		st, body = bob.json(http.MethodPost, "/admin/domains/"+externalHost+"/approve", nil)
		if st != http.StatusNotFound {
			t.Fatalf("user approves: %d %v", st, body)
		}
	})

	t.Run("an approved domain gets a certificate and serves the site", func(t *testing.T) {
		st, body := admin.json(http.MethodPost, "/admin/domains/"+externalHost+"/approve", nil)
		must(t, st, http.StatusOK, body, "approve")
		if str(body, "status") != "active" || str(body, "owner_email") != "ann@dropsite.localhost" {
			t.Fatalf("approved %v", body)
		}
		s.expectBody(externalHost, "/", 200, "<h1>external</h1>")
		names, err := handshake(externalHost)
		if err != nil || !slices.Contains(names, externalHost) {
			t.Fatalf("certificate %v, %v", names, err)
		}
		st, body = ann.json(http.MethodGet, "/sites/"+siteID, nil)
		must(t, st, http.StatusOK, body, "get site")
		urls, _ := body["urls"].([]any)
		found := false
		for _, u := range urls {
			found = found || hostOf(u.(string)) == externalHost
		}
		if !found {
			t.Fatalf("approved domain missing from urls %v", urls)
		}
	})

	t.Run("withdrawing the approval stops serving at once", func(t *testing.T) {
		st, body := admin.json(http.MethodPost, "/admin/domains/"+externalHost+"/reject",
			map[string]any{"reason": "campaign is over"})
		must(t, st, http.StatusOK, body, "reject")
		s.expectBody(externalHost, "/", 404, "")
		st, body = ann.json(http.MethodGet, "/sites/"+siteID+"/hostnames", nil)
		must(t, st, http.StatusOK, body, "list hostnames")
		items, _ := body["items"].([]any)
		last, _ := items[len(items)-1].(map[string]any)
		if str(last, "status") != "rejected" || str(last, "reason") != "campaign is over" {
			t.Fatalf("owner sees %v", last)
		}
	})

	t.Run("a removed domain is free for another site", func(t *testing.T) {
		st, body := ann.do(http.MethodDelete, "/sites/"+siteID+"/hostnames/"+externalHost, nil, "")
		if st != http.StatusNoContent {
			t.Fatalf("remove: %d %v", st, body)
		}
		st, body = bob.json(http.MethodGet, "/sites", nil)
		must(t, st, http.StatusOK, body, "list sites")
		items, _ := body["items"].([]any)
		thief, _ := items[0].(map[string]any)
		st, body = bob.json(http.MethodPost, "/sites/"+str(thief, "id")+"/hostnames", map[string]any{"hostname": externalHost})
		must(t, st, http.StatusCreated, body, "add released domain")
		if names, err := handshake(externalHost); err == nil && slices.Contains(names, externalHost) {
			// The certificate issued earlier may still be cached by Caddy, but
			// the pending domain has no by-host entry, so nothing is served.
			s.expectBody(externalHost, "/", 404, "")
		}
	})
}
