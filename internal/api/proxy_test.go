package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

// proxyUpstream answers every https host the proxy dials and records the
// Authorization header it received.
func proxyUpstream(t *testing.T) (http.RoundTripper, *string) {
	t.Helper()
	var auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "x=1")
		_, _ = w.Write([]byte("data: hi\n\n"))
	}))
	t.Cleanup(srv.Close)
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com"
	addr := srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	return tr, &auth
}

type proxyEndpointOut struct {
	Name      string   `json:"name"`
	Hosts     []string `json:"allowed_hosts"`
	Methods   []string `json:"methods"`
	SecretSet bool     `json:"secret_set"`
	SecretURL string   `json:"secret_url"`
	CallURL   string   `json:"call_url"`
	Usage     struct {
		Requests int64 `json:"requests"`
		Rejected int64 `json:"rejected"`
	} `json:"usage"`
}

func TestProxyEndpointsAPI(t *testing.T) {
	tr, auth := proxyUpstream(t)
	h := newHarness(t, testenv.WithProxyTransport(tr))
	ann, owner := h.user("ann@pp.test")
	_, other := h.user("bob@pp.test")
	site := h.createSite(owner, "Bot", "")
	base := "/api/v1/sites/" + site.ID + "/proxy/endpoints"
	cfg := map[string]any{"upstream_url": "https://api.example.com/v1", "auth": "bearer", "methods": []string{"POST"}}

	withSecret := map[string]any{"upstream_url": "https://api.example.com/v1", "auth": "bearer", "secret": "sk-1"}
	rec := h.do(http.MethodPut, base+"/ai", owner, withSecret)
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "proxy_secret_ui_only") || !strings.Contains(rec.Body.String(), "/proxy#proxy-ai") {
		t.Fatalf("secret refusal: %s", rec.Body)
	}
	wantStatus(t, h.do(http.MethodPut, base+"/ai", other, withSecret), http.StatusNotFound)

	rec = h.do(http.MethodPut, base+"/ai", owner, cfg)
	wantStatus(t, rec, http.StatusCreated)
	var ep proxyEndpointOut
	if err := json.Unmarshal(rec.Body.Bytes(), &ep); err != nil {
		t.Fatal(err)
	}
	if ep.SecretSet || ep.Hosts[0] != "api.example.com" || !strings.HasSuffix(ep.CallURL, "/_dropsite/proxy/ai/") ||
		!strings.Contains(ep.SecretURL, "/sites/"+site.ID+"/proxy") {
		t.Fatalf("created: %+v", ep)
	}
	wantStatus(t, h.do(http.MethodPut, base+"/ai", owner, cfg), http.StatusOK)
	rec = h.do(http.MethodPut, base+"/bad", owner, map[string]any{"upstream_url": "https://127.0.0.1", "auth": "bearer"})
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "proxy_invalid") {
		t.Fatalf("invalid: %s", rec.Body)
	}
	wantStatus(t, h.do(http.MethodGet, base, other, nil), http.StatusNotFound)

	// The visitor side: no key yet, then a key pasted in the web interface.
	host := site.ID + "." + testenv.BaseDomain
	call := func(method, path string, opts ...reqOpt) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"q":1}`))
		req.Host = host
		req.RemoteAddr = "198.51.100.20:5000"
		for _, o := range opts {
			o(req)
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, req)
		return rec
	}
	rec = call(http.MethodPost, "/_dropsite/proxy/ai/chat")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), "proxy_secret_missing") {
		t.Fatalf("no key: %s", rec.Body)
	}
	if _, err := h.env.Svc.SetProxySecret(context.Background(), ann, site.ID, "ai", "sk-live-42"); err != nil {
		t.Fatal(err)
	}
	rec = call(http.MethodPost, "/_dropsite/proxy/ai/chat", withHeader("Origin", "https://"+host),
		withHeader("Cookie", "__Host-dropsite_access=v"))
	wantStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "data: hi\n\n" || rec.Header().Get("Set-Cookie") != "" ||
		rec.Header().Get("Content-Type") != "text/event-stream" || *auth != "Bearer sk-live-42" {
		t.Fatalf("forwarded: %d %q %v auth=%q", rec.Code, rec.Body, rec.Header(), *auth)
	}
	wantStatus(t, call(http.MethodPost, "/_dropsite/proxy/ai/chat", withHeader("Origin", "https://evil.example")), http.StatusForbidden)
	wantStatus(t, call(http.MethodGet, "/_dropsite/proxy/ai/chat"), http.StatusMethodNotAllowed)
	wantStatus(t, call(http.MethodPost, "/_dropsite/proxy/nope/chat"), http.StatusNotFound)

	rec = h.do(http.MethodGet, base, owner, nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "sk-live-42") {
		t.Fatal("API response contains the key")
	}
	var list struct {
		Items []proxyEndpointOut `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %s %v", rec.Body, err)
	}
	if got := list.Items[0]; !got.SecretSet || got.Usage.Requests != 1 || got.Usage.Rejected != 3 {
		t.Fatalf("listed: %+v", got)
	}

	wantStatus(t, h.do(http.MethodDelete, base+"/ai", other, nil), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodDelete, base+"/ai", owner, nil), http.StatusNoContent)
	wantStatus(t, h.do(http.MethodDelete, base+"/ai", owner, nil), http.StatusNotFound)
	if strings.Contains(h.logs.String(), "sk-live-42") {
		t.Fatal("key in the logs")
	}
}
