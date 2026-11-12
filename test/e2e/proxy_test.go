//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestSecretProxyRouting checks the Caddy side of the secret proxy: the
// path reaches control on an open site, stays behind the password of a
// protected one, and the body limit applies. No upstream is contacted: the
// endpoint has no key, which the web interface alone can set.
func TestSecretProxyRouting(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}
	ann := createUser(t, s, admin, "proxy@dropsite.localhost")

	st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": "Proxy"})
	must(t, st, http.StatusCreated, body, "create site")
	id := str(body, "id")
	st, body = ann.deploy(id, makeZip(t, site(map[string]string{"index.html": "<h1>Proxy</h1>"})...))
	must(t, st, http.StatusCreated, body, "deploy")
	host := id + "." + baseDomain
	s.expectBody(host, "/", 200, "Proxy")

	cfg := map[string]any{"upstream_url": "https://api.openai.com/v1", "auth": "bearer", "methods": []string{"POST"}}
	st, body = ann.json(http.MethodPut, "/sites/"+id+"/proxy/endpoints/ai", cfg)
	must(t, st, http.StatusCreated, body, "create endpoint")
	if body["secret_set"] != false || !strings.Contains(str(body, "secret_url"), "/proxy#proxy-ai") {
		t.Fatalf("endpoint: %v", body)
	}
	withKey := map[string]any{"upstream_url": "https://api.openai.com/v1", "auth": "bearer", "secret": "sk-1"}
	st, body = ann.json(http.MethodPut, "/sites/"+id+"/proxy/endpoints/ai", withKey)
	if st != http.StatusBadRequest || errCode(body) != "proxy_secret_ui_only" {
		t.Fatalf("secret through the API: %d %v", st, body)
	}

	call := func(method, origin string, payload []byte) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(s.ctx, method, "https://"+host+":"+httpsPort+"/_dropsite/proxy/ai/chat/completions",
			bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatalf("%s proxy: %v", method, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	errOf := func(body string) string {
		var out map[string]any
		_ = json.Unmarshal([]byte(body), &out)
		return errCode(out)
	}

	// The stack listens on a non-default port, so the page's origin carries it.
	resp, b := call(http.MethodPost, "https://"+host+":"+httpsPort, []byte(`{"q":1}`))
	if resp.StatusCode != http.StatusServiceUnavailable || errOf(b) != "proxy_secret_missing" {
		t.Fatalf("no key: %d %s", resp.StatusCode, b)
	}
	if resp.Header.Get("X-Robots-Tag") == "" {
		t.Error("proxy errors must not be indexed")
	}
	if resp, b = call(http.MethodPost, "https://evil.example", []byte(`{}`)); resp.StatusCode != http.StatusForbidden || errOf(b) != "proxy_origin" {
		t.Fatalf("other origin: %d %s", resp.StatusCode, b)
	}
	if resp, b = call(http.MethodDelete, "", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("method: %d %s", resp.StatusCode, b)
	}
	if resp, b = call(http.MethodPost, "", bytes.Repeat([]byte("a"), 2<<20)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body: %d %.80s", resp.StatusCode, b)
	}

	st, body = ann.json(http.MethodPatch, "/sites/"+id, map[string]any{"password": "visitor-pass-1"})
	must(t, st, http.StatusOK, body, "protect")
	s.eventually("proxy behind the password", func() error {
		resp, b := call(http.MethodPost, "", []byte(`{}`))
		if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "/_dropsite/login") ||
			strings.Contains(b, "proxy_secret_missing") {
			return fmt.Errorf("status %d, location %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		return nil
	})
	s.expectBody(host, "/_dropsite/login", 200, "")

	st, body = ann.json(http.MethodGet, "/sites/"+id+"/proxy/endpoints", nil)
	must(t, st, http.StatusOK, body, "list endpoints")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("endpoints: %v", body)
	}
	usage, _ := items[0].(map[string]any)["usage"].(map[string]any)
	if usage["rejected"] == float64(0) {
		t.Fatalf("usage not counted: %v", usage)
	}
}
