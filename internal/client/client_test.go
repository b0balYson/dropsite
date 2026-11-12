package client

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "pp_secret", opts...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestMeSendsAuthAndClientHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pp_secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Pushpage-Client"); got != UserAgent() {
			t.Errorf("X-Pushpage-Client = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != UserAgent() {
			t.Errorf("User-Agent = %q", got)
		}
		writeJSON(w, 200, map[string]any{
			"user":       map[string]any{"id": "u1", "email": "a@b.c", "role": "user"},
			"limits":     map[string]any{"max_site_bytes": 100, "max_file_bytes": 10, "max_files": 5},
			"site_count": 2,
		})
	})
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.User.Email != "a@b.c" || me.Limits.MaxFiles != 5 || me.SiteCount != 2 {
		t.Fatalf("unexpected me: %+v", me)
	}
	if !strings.HasPrefix(UserAgent(), "cli/") {
		t.Fatalf("UserAgent = %q", UserAgent())
	}
}

func TestNewStripsAPIPrefixAndSlash(t *testing.T) {
	c := New("https://api.example.com/api/v1/", "t")
	if c.Server() != "https://api.example.com" {
		t.Fatalf("Server() = %q", c.Server())
	}
}

func TestSiteEndpoints(t *testing.T) {
	var calls []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/sites":
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"id": "s1", "url": "https://x"}}, "total": 1})
		case "POST /api/v1/sites":
			if !strings.Contains(string(body), `"title":"Hello"`) {
				t.Errorf("create body = %s", body)
			}
			writeJSON(w, 201, map[string]any{"id": "s1", "title": "Hello"})
		case "GET /api/v1/sites/a b":
			writeJSON(w, 200, map[string]any{"id": "s1", "slug": "a b"})
		case "PATCH /api/v1/sites/s1":
			if string(body) != `{"slug":""}` {
				t.Errorf("patch body = %s", body)
			}
			writeJSON(w, 200, map[string]any{"id": "s1", "slug": nil})
		case "DELETE /api/v1/sites/s1":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/sites/s1/deploys":
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"id": "d2", "current": true}}, "total": 1})
		case "POST /api/v1/sites/s1/rollback":
			if string(body) != `{"deploy_id":"d1"}` {
				t.Errorf("rollback body = %s", body)
			}
			writeJSON(w, 200, map[string]any{"deploy_id": "d1", "url": "https://x"})
		case "GET /api/v1/tokens":
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"id": "t1", "prefix": "pp_ab"}}, "total": 1})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	ctx := context.Background()
	list, err := c.ListSites(ctx, ListOptions{Limit: 10, Offset: 20})
	must(t, err)
	if list.Total != 1 || list.Items[0].ID != "s1" {
		t.Fatalf("list = %+v", list)
	}
	site, err := c.CreateSite(ctx, CreateSiteRequest{Title: "Hello"})
	must(t, err)
	if site.Title != "Hello" {
		t.Fatalf("site = %+v", site)
	}
	site, err = c.GetSite(ctx, "a b")
	must(t, err)
	if site.Slug == nil || *site.Slug != "a b" {
		t.Fatalf("site = %+v", site)
	}
	empty := ""
	site, err = c.UpdateSite(ctx, "s1", UpdateSiteRequest{Slug: &empty})
	must(t, err)
	if site.Slug != nil {
		t.Fatalf("slug not removed: %+v", site)
	}
	must(t, c.DeleteSite(ctx, "s1"))
	deploys, err := c.ListDeploys(ctx, "s1")
	must(t, err)
	if !deploys.Items[0].Current {
		t.Fatalf("deploys = %+v", deploys)
	}
	rb, err := c.Rollback(ctx, "s1", "d1")
	must(t, err)
	if rb.DeployID != "d1" {
		t.Fatalf("rollback = %+v", rb)
	}
	tokens, err := c.ListTokens(ctx)
	must(t, err)
	if tokens.Items[0].Prefix != "pp_ab" {
		t.Fatalf("tokens = %+v", tokens)
	}
	if calls[0] != "GET /api/v1/sites?limit=10&offset=20" {
		t.Fatalf("list query = %q", calls[0])
	}
}

func TestAPIErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		header  string
		body    string
		code    string
		message string
		retry   time.Duration
	}{
		{"unauthorized", 401, "", `{"error":{"code":"unauthorized","message":"authentication required"}}`, "unauthorized", "authentication required", 0},
		{"rate limited", 429, "17", `{"error":{"code":"rate_limited","message":"too many requests"}}`, "rate_limited", "too many requests", 17 * time.Second},
		{"details", 413, "", `{"error":{"code":"archive_too_large","message":"too big","details":{"limit":5}}}`, "archive_too_large", "too big", 0},
		{"plain text", 502, "", "bad gateway from proxy", "", "bad gateway from proxy", 0},
		{"html", 503, "", "<html>oops</html>", "", "Service Unavailable", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.Me(context.Background())
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if ae.Status != tt.status || ae.Code != tt.code || ae.Message != tt.message || ae.RetryAfter != tt.retry {
				t.Fatalf("got %+v", ae)
			}
			if ae.Error() == "" {
				t.Fatal("empty Error()")
			}
		})
	}
}

func TestAPIErrorHelpers(t *testing.T) {
	e := &APIError{Status: 404, Code: "not_found", Message: "not found", Details: map[string]any{"x": 1}}
	if !e.IsNotFound() || e.IsUnauthorized() {
		t.Fatal("404 helpers")
	}
	if got := e.Error(); got != "not found (not_found, HTTP 404)" {
		t.Fatalf("Error() = %q", got)
	}
	if !(&APIError{Status: 401}).IsUnauthorized() {
		t.Fatal("401 helper")
	}
	long := strings.Repeat("x", 300)
	if got := plainMessage([]byte(long), 500); len(got) != maxPlainMessage+3 {
		t.Fatalf("plainMessage not truncated: %d", len(got))
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	date := now.Add(30 * time.Second).Format(http.TimeFormat)
	tests := map[string]time.Duration{"": 0, "5": 5 * time.Second, "-3": 0, "junk": 0, date: 30 * time.Second}
	for in, want := range tests {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNetworkErrorIsWrapped(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := New(srv.URL, "t")
	_, err := c.Me(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("err = %v", err)
	}
	var ae *APIError
	if errors.As(err, &ae) {
		t.Fatal("network error must not be an APIError")
	}
}

func TestTimeout(t *testing.T) {
	block := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}, WithTimeout(50*time.Millisecond))
	defer close(block)
	_, err := c.Me(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestBadJSONResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{not json")
	}, WithHTTPClient(&http.Client{}))
	if _, err := c.Me(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorsFromEveryMethod(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "internal", "message": "boom"}})
	})
	ctx := context.Background()
	calls := map[string]func() error{
		"ListSites":   func() error { _, err := c.ListSites(ctx, ListOptions{}); return err },
		"CreateSite":  func() error { _, err := c.CreateSite(ctx, CreateSiteRequest{}); return err },
		"GetSite":     func() error { _, err := c.GetSite(ctx, "s"); return err },
		"UpdateSite":  func() error { _, err := c.UpdateSite(ctx, "s", UpdateSiteRequest{}); return err },
		"DeleteSite":  func() error { return c.DeleteSite(ctx, "s") },
		"ListDeploys": func() error { _, err := c.ListDeploys(ctx, "s"); return err },
		"Rollback":    func() error { _, err := c.Rollback(ctx, "s", ""); return err },
		"ListTokens":  func() error { _, err := c.ListTokens(ctx); return err },
		"Deploy":      func() error { _, err := c.Deploy(ctx, "s", strings.NewReader("x"), nil); return err },
	}
	for name, call := range calls {
		var ae *APIError
		if err := call(); !errors.As(err, &ae) || ae.Status != 500 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithRootCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"id":"1","email":"a@b.co","role":"user","status":"active"},"limits":{},"site_count":0}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(srv.URL, "t").Me(context.Background()); err == nil {
		t.Fatal("untrusted certificate must fail without the CA file")
	}
	opt, err := WithRootCAFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(srv.URL, "t", opt).Me(context.Background()); err != nil {
		t.Fatalf("trusted via CA file: %v", err)
	}
	bad := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("nope"), 0o600)
	if _, err := WithRootCAFile(bad); err == nil {
		t.Fatal("non-PEM file must fail")
	}
	if _, err := WithRootCAFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file must fail")
	}
}
