package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// seenRequest is what the fake upstream received.
type seenRequest struct {
	Method, Host, URI string
	Header            http.Header
	Body              string
}

type upstream struct {
	srv     *httptest.Server
	mu      sync.Mutex
	seen    []seenRequest
	handler http.HandlerFunc
}

// newUpstream serves every https host through one TLS test server: the
// transport dials it whatever the name.
func newUpstream(t *testing.T) (*upstream, http.RoundTripper) {
	t.Helper()
	u := &upstream{}
	u.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.seen = append(u.seen, seenRequest{Method: r.Method, Host: r.Host, URI: r.RequestURI, Header: r.Header.Clone(), Body: string(b)})
		h := u.handler
		u.mu.Unlock()
		if h != nil {
			h(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "up=1")
		w.Header().Set("X-Upstream-Secret", "internal")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(u.srv.Close)
	tr := u.srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com"
	addr := u.srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	return u, tr
}

func (u *upstream) last(t *testing.T) seenRequest {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.seen) == 0 {
		t.Fatal("upstream was not called")
	}
	return u.seen[len(u.seen)-1]
}

func (u *upstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.seen)
}

type proxyRig struct {
	*env
	up    *upstream
	owner Actor
	host  string
}

func newProxyRig(t *testing.T) *proxyRig {
	t.Helper()
	e := newEnv(t)
	up, tr := newUpstream(t)
	e.svc.proxyClient = newProxyClient(tr)
	ann := e.user("ann@pp.test")
	if _, err := e.svc.CreateSite(context.Background(), ann, "Bot", "bot"); err != nil {
		t.Fatal(err)
	}
	return &proxyRig{env: e, up: up, owner: ann, host: "bot." + testBase}
}

func (r *proxyRig) endpoint(name string, c domain.ProxyConfig, secret string) {
	r.t.Helper()
	ctx := context.Background()
	if _, _, err := r.svc.PutProxyEndpoint(ctx, r.owner, "bot", name, c); err != nil {
		r.t.Fatal(err)
	}
	if secret != "" {
		if _, err := r.svc.SetProxySecret(ctx, r.owner, "bot", name, secret); err != nil {
			r.t.Fatal(err)
		}
	}
}

func (r *proxyRig) call(c ProxyCall) (*ProxyResponse, string, error) {
	r.t.Helper()
	if c.Host == "" {
		c.Host = r.host
	}
	if c.Method == "" {
		c.Method = http.MethodPost
	}
	if c.IP == "" {
		c.IP = "203.0.113.7"
	}
	if c.Header == nil {
		c.Header = http.Header{}
	}
	resp, err := r.svc.OpenProxy(context.Background(), c)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

func TestProxyForwardsWithBearer(t *testing.T) {
	r := newProxyRig(t)
	cfg := domain.ProxyConfig{UpstreamURL: "https://api.example.com/v1?mode=x", Auth: domain.ProxyAuthBearer,
		Methods: []string{"POST"}, Paths: []string{"/chat"}, Enabled: true}
	r.endpoint("ai", cfg, "sk-secret-1")
	hdr := http.Header{
		"Cookie": {"__Host-dropsite_access=abc"}, "Authorization": {"Bearer visitor"}, "Content-Type": {"application/json"},
		"X-Forwarded-For": {"1.2.3.4"}, "Connection": {"X-Drop"}, "X-Drop": {"1"}, "Origin": {"https://" + r.host},
		"Accept-Encoding": {"br"}, "Openai-Beta": {"assistants=v2"},
	}
	resp, body, err := r.call(ProxyCall{Name: "ai", Path: "/chat/completions", RawQuery: "stream=false", Header: hdr,
		Origin: "https://" + r.host, FetchSite: "same-origin", Body: strings.NewReader(`{"q":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || body != `{"ok":true}` {
		t.Fatalf("response %d %s", resp.Status, body)
	}
	for _, h := range []string{"Set-Cookie", "X-Upstream-Secret"} {
		if resp.Header.Get(h) != "" {
			t.Errorf("response header %s passed", h)
		}
	}
	if resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") ||
		resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("response headers %v", resp.Header)
	}
	got := r.up.last(t)
	if got.Method != "POST" || got.Host != "api.example.com" || got.URI != "/v1/chat/completions?mode=x&stream=false" ||
		got.Body != `{"q":1}` {
		t.Fatalf("upstream got %+v", got)
	}
	if got.Header.Get("Authorization") != "Bearer sk-secret-1" || got.Header.Get("Openai-Beta") != "assistants=v2" {
		t.Errorf("auth/custom headers: %v", got.Header)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Drop", "Origin"} {
		if got.Header.Get(h) != "" {
			t.Errorf("visitor header %s forwarded: %v", h, got.Header)
		}
	}
	list, _ := r.svc.ListProxyEndpoints(context.Background(), r.owner, "bot")
	if u := list[0].Endpoint.Usage; u.Requests != 1 || u.Errors != 0 || u.LastStatus != 200 || u.LastUsedAt == nil {
		t.Fatalf("usage %+v", u)
	}
}

func TestProxyPlacements(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("q", domain.ProxyConfig{UpstreamURL: "https://maps.example.com/api", Auth: domain.ProxyAuthQuery,
		AuthName: "apikey", Methods: []string{"GET"}, Enabled: true}, "k1")
	r.endpoint("h", domain.ProxyConfig{UpstreamURL: "https://crm.example.com", Auth: domain.ProxyAuthHeader,
		AuthName: "X-Api-Key", Enabled: true}, "k2")
	r.endpoint("tg", domain.ProxyConfig{UpstreamURL: "https://api.telegram.example.com/bot{secret}", Auth: domain.ProxyAuthPath,
		Paths: []string{"/sendMessage"}, Enabled: true}, "123:AB/C")

	if _, _, err := r.call(ProxyCall{Name: "q", Method: "GET", Path: "/geo", RawQuery: "apikey=visitor&city=Kazan"}); err != nil {
		t.Fatal(err)
	}
	if got := r.up.last(t); got.URI != "/api/geo?apikey=k1&city=Kazan" {
		t.Fatalf("query placement: %s", got.URI)
	}
	if _, _, err := r.call(ProxyCall{Name: "h", Path: "/leads", Header: http.Header{"X-Api-Key": {"visitor"}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.up.last(t); got.Header.Values("X-Api-Key")[0] != "k2" || len(got.Header.Values("X-Api-Key")) != 1 {
		t.Fatalf("header placement: %v", got.Header)
	}
	if _, _, err := r.call(ProxyCall{Name: "tg", Path: "/sendMessage"}); err != nil {
		t.Fatal(err)
	}
	if got := r.up.last(t); got.URI != "/bot123:AB%2FC/sendMessage" {
		t.Fatalf("path placement: %s", got.URI)
	}
}

func TestProxyRefusals(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("ai", domain.ProxyConfig{UpstreamURL: "https://api.example.com/v1", Auth: domain.ProxyAuthBearer,
		Methods: []string{"POST"}, Paths: []string{"/chat"}, Enabled: true}, "sk")
	r.endpoint("nokey", domain.ProxyConfig{UpstreamURL: "https://api.example.com/v1", Auth: domain.ProxyAuthBearer, Enabled: true}, "")
	r.endpoint("off", domain.ProxyConfig{UpstreamURL: "https://api.example.com/v1", Auth: domain.ProxyAuthBearer}, "sk")
	cases := []struct {
		name string
		c    ProxyCall
		code string
	}{
		{"origin", ProxyCall{Name: "ai", Path: "/chat", Origin: "https://evil.example"}, domain.CodeProxyOrigin},
		{"fetch site", ProxyCall{Name: "ai", Path: "/chat", FetchSite: "cross-site"}, domain.CodeProxyOrigin},
		{"method", ProxyCall{Name: "ai", Path: "/chat", Method: "DELETE"}, domain.CodeProxyMethod},
		{"path", ProxyCall{Name: "ai", Path: "/files"}, domain.CodeProxyPath},
		{"prefix boundary", ProxyCall{Name: "ai", Path: "/chatty"}, domain.CodeProxyPath},
		{"dot dot", ProxyCall{Name: "ai", Path: "/chat/%2e%2e/files"}, domain.CodeProxyPath},
		{"encoded slash", ProxyCall{Name: "ai", Path: "/chat%2f..%2ffiles"}, domain.CodeProxyPath},
		{"no key", ProxyCall{Name: "nokey", Path: "/x"}, domain.CodeProxySecretMissing},
		{"too large", ProxyCall{Name: "ai", Path: "/chat", Body: bytes.NewReader(make([]byte, ProxyMaxRequestBytes+1))}, domain.CodeProxyTooLarge},
	}
	for _, tc := range cases {
		_, _, err := r.call(tc.c)
		if domain.AsError(err).Code != tc.code {
			t.Errorf("%s: want %s, got %v", tc.name, tc.code, err)
		}
	}
	for _, c := range []ProxyCall{{Name: "off"}, {Name: "nope"}, {Name: "ai", Host: "other." + testBase}, {Name: "Bad Name"}} {
		if _, _, err := r.call(c); domain.AsError(err).Kind != domain.KindNotFound {
			t.Errorf("%+v: want not found, got %v", c, err)
		}
	}
	if r.up.count() != 0 {
		t.Fatalf("upstream called %d times", r.up.count())
	}
	list, _ := r.svc.ListProxyEndpoints(context.Background(), r.owner, "bot")
	for _, v := range list {
		if v.Endpoint.Name == "ai" && (v.Endpoint.Usage.Rejected != 8 || v.Endpoint.Usage.Requests != 0) {
			t.Fatalf("usage %+v", v.Endpoint.Usage)
		}
	}
}

func TestProxyRateLimits(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("ai", domain.ProxyConfig{UpstreamURL: "https://api.example.com", Auth: domain.ProxyAuthBearer, Enabled: true}, "sk")
	for i := range ProxyCallsPerIP {
		if _, _, err := r.call(ProxyCall{Name: "ai"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	_, _, err := r.call(ProxyCall{Name: "ai"})
	if e := domain.AsError(err); e.Kind != domain.KindRateLimited || e.RetryAfter <= 0 {
		t.Fatalf("61st call: %v", err)
	}
	// Refused cross-site calls do not use the quota of another address.
	if _, _, err := r.call(ProxyCall{Name: "ai", IP: "198.51.100.9", Origin: "https://evil.example"}); domain.AsError(err).Code != domain.CodeProxyOrigin {
		t.Fatal(err)
	}
	if _, _, err := r.call(ProxyCall{Name: "ai", IP: "198.51.100.9"}); err != nil {
		t.Fatalf("another address: %v", err)
	}
}

func TestProxyRedirects(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("ai", domain.ProxyConfig{UpstreamURL: "https://api.example.com", Auth: domain.ProxyAuthHeader, AuthName: "X-Key",
		AllowedHosts: []string{"files.example.com"}, Enabled: true}, "sk")
	r.up.handler = func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Host == "api.example.com" && req.URL.Path == "/ok":
			http.Redirect(w, req, "https://files.example.com/final", http.StatusFound)
		case req.Host == "api.example.com" && req.URL.Path == "/evil":
			http.Redirect(w, req, "https://evil.example.org/steal", http.StatusTemporaryRedirect)
		case req.Host == "api.example.com" && req.URL.Path == "/plain":
			http.Redirect(w, req, "http://files.example.com/final", http.StatusFound)
		case req.URL.Path == "/loop":
			http.Redirect(w, req, "/loop", http.StatusFound)
		default:
			_, _ = w.Write([]byte("final"))
		}
	}
	resp, body, err := r.call(ProxyCall{Name: "ai", Path: "/ok", Body: strings.NewReader("x")})
	if err != nil || body != "final" || resp.Header.Get("Location") != "" {
		t.Fatalf("allowed redirect: %v %q", err, body)
	}
	if got := r.up.last(t); got.Host != "files.example.com" || got.Method != "GET" || got.Header.Get("X-Key") != "sk" || got.Body != "" {
		t.Fatalf("after redirect: %+v", got)
	}
	n := r.up.count()
	for _, p := range []string{"/evil", "/plain", "/loop"} {
		if _, _, err := r.call(ProxyCall{Name: "ai", Path: p}); domain.AsError(err).Code != domain.CodeProxyUpstream {
			t.Errorf("%s: want proxy_upstream, got %v", p, err)
		}
	}
	for _, s := range r.up.seen[n:] {
		if s.Host == "evil.example.org" {
			t.Fatal("secret sent to a host outside the list")
		}
	}
}

func TestProxyResponseLimits(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("ai", domain.ProxyConfig{UpstreamURL: "https://api.example.com", Auth: domain.ProxyAuthBearer, Enabled: true}, "sk")
	big := bytes.Repeat([]byte("a"), ProxyMaxResponseBytes+10)
	r.up.handler = func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/declared" {
			w.Header().Set("Content-Length", "20000000")
		}
		if f, ok := w.(http.Flusher); ok && req.URL.Path == "/stream" {
			_, _ = w.Write(big[:10])
			f.Flush()
		}
		_, _ = w.Write(big)
	}
	if _, _, err := r.call(ProxyCall{Name: "ai", Path: "/declared"}); domain.AsError(err).Code != domain.CodeProxyUpstream {
		t.Fatalf("declared size: %v", err)
	}
	if _, _, err := r.call(ProxyCall{Name: "ai", Path: "/stream"}); !errors.Is(err, ErrProxyResponseTooLarge) {
		t.Fatalf("streamed size: %v", err)
	}
	r.up.handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }
	resp, _, err := r.call(ProxyCall{Name: "ai"})
	if err != nil || resp.Status != http.StatusBadGateway {
		t.Fatalf("upstream 502: %v", err)
	}
	list, _ := r.svc.ListProxyEndpoints(context.Background(), r.owner, "bot")
	if u := list[0].Endpoint.Usage; u.Errors != 2 || u.LastStatus != 502 {
		t.Fatalf("usage %+v", u)
	}
}

func TestProxyTransportRefusesInternalAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	client := newProxyClient(newProxyTransport([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}))
	resp, err := client.Get(srv.URL) //nolint:noctx // test
	if err == nil {
		_ = resp.Body.Close()
	}
	if err == nil || !isBlockedDial(err) {
		t.Fatalf("loopback reached: %v", err)
	}
	if got := classifyProxyErr(context.Background(), err); domain.AsError(got).Code != domain.CodeProxyUpstream {
		t.Fatalf("classified as %v", got)
	}
}

// Override headers would let a visitor run a method or reach a path the
// endpoint does not allow on upstreams that honour them.
func TestProxyDropsMethodAndURLOverrides(t *testing.T) {
	r := newProxyRig(t)
	cfg := domain.ProxyConfig{UpstreamURL: "https://api.example.com/v1", Auth: domain.ProxyAuthBearer,
		Methods: []string{"POST"}, Paths: []string{"/chat"}, Enabled: true}
	r.endpoint("ai", cfg, "sk-secret-1")
	overrides := []string{"X-Http-Method-Override", "X-Http-Method", "X-Method-Override", "X-Original-Method",
		"X-Original-Url", "X-Rewrite-Url"}
	hdr := http.Header{}
	for _, h := range overrides {
		hdr.Set(h, "DELETE")
	}
	if _, _, err := r.call(ProxyCall{Name: "ai", Path: "/chat", Header: hdr, Origin: "https://" + r.host,
		FetchSite: "same-origin", Body: strings.NewReader(`{}`)}); err != nil {
		t.Fatal(err)
	}
	got := r.up.last(t)
	for _, h := range overrides {
		if got.Header.Get(h) != "" {
			t.Errorf("visitor header %s forwarded", h)
		}
	}
}
