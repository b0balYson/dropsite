package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// fakeResolver maps every host to fixed addresses.
type fakeResolver struct {
	addrs []netip.Addr
	err   error
}

func (f fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return f.addrs, f.err
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, v := range s {
		out = append(out, netip.MustParseAddr(v))
	}
	return out
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.5": false, "172.16.1.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "::1": false, "fe80::1": false, "fc00::1": false, "::ffff:10.0.0.1": false,
		"64:ff9b::a00:1": false, "2002:a00:1::": false, "224.0.0.1": false, "198.18.0.1": false, "240.0.0.1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestIsMetadataURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://chatgpt.com/oauth/client.json": true,
		"https://a.example":                     false,
		"https://a.example/":                    false,
		"http://a.example/c.json":               false,
		"https://u@a.example/c.json":            false,
		"https://a.example/c.json#x":            false,
		"https://a.example/x/..":                false,
		"dsc_abc":                               false,
	} {
		if IsMetadataURL(in) != want {
			t.Errorf("%s", in)
		}
	}
}

// cimdServer serves a metadata document over TLS on 127.0.0.1; the fetcher
// reaches it through a fake resolver for "example.com" (in the test cert).
type cimdServer struct {
	srv   *httptest.Server
	body  atomic.Value // string
	hits  atomic.Int32
	extra func(w http.ResponseWriter)
}

func newCIMDServer(t *testing.T) *cimdServer {
	t.Helper()
	c := &cimdServer{}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		if c.extra != nil {
			c.extra(w)
			return
		}
		_, _ = io.WriteString(w, c.body.Load().(string))
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *cimdServer) clientID() string {
	_, port, _ := net.SplitHostPort(c.srv.Listener.Addr().String())
	return "https://example.com:" + port + "/client.json"
}

func (c *cimdServer) doc(id string, redirects ...string) {
	b, _ := json.Marshal(map[string]any{"client_id": id, "client_name": "Chat", "redirect_uris": redirects})
	c.body.Store(string(b))
}

func testFetcher(c *cimdServer, allow func(netip.Addr) bool, res Resolver) *fetcher {
	pool := x509.NewCertPool()
	pool.AddCert(c.srv.Certificate())
	return newFetcher(res, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, allow)
}

func allowAll(netip.Addr) bool { return true }

func TestCIMDFetchAndCache(t *testing.T) {
	c := newCIMDServer(t)
	id := c.clientID()
	c.doc(id, "https://chat.example/cb")
	f := testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")})
	got, err := f.get(context.Background(), id)
	if err != nil || got.Name != "Chat" || !got.Metadata || got.RedirectURIs[0] != "https://chat.example/cb" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := f.get(context.Background(), id); err != nil || c.hits.Load() != 1 {
		t.Fatalf("not cached: %d %v", c.hits.Load(), err)
	}
	f.now = func() time.Time { return time.Now().Add(cimdMaxTTL + time.Minute) }
	if _, err := f.get(context.Background(), id); err != nil || c.hits.Load() != 2 {
		t.Fatalf("cache not expired: %d %v", c.hits.Load(), err)
	}
}

func TestCIMDRejections(t *testing.T) {
	c := newCIMDServer(t)
	id := c.clientID()
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func()
		f     func() *fetcher
	}{
		{"private address", func() { c.doc(id, "https://chat.example/cb") },
			func() *fetcher { return testFetcher(c, nil, fakeResolver{addrs: addrs("127.0.0.1")}) }},
		{"one private among public", func() { c.doc(id, "https://chat.example/cb") },
			func() *fetcher { return testFetcher(c, nil, fakeResolver{addrs: addrs("8.8.8.8", "10.0.0.5")}) }},
		{"resolver error", func() {}, func() *fetcher {
			return testFetcher(c, allowAll, fakeResolver{err: errors.New("nxdomain")})
		}},
		{"no addresses", func() {}, func() *fetcher { return testFetcher(c, allowAll, fakeResolver{}) }},
		{"client_id mismatch", func() { c.doc("https://other.example/c.json", "https://chat.example/cb") },
			func() *fetcher { return testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")}) }},
		{"bad redirect", func() { c.doc(id, "http://chat.example/cb") },
			func() *fetcher { return testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")}) }},
		{"no redirects listed", func() { c.doc(id) },
			func() *fetcher { return testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")}) }},
		{"not json", func() { c.body.Store("<html>") },
			func() *fetcher { return testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")}) }},
		{"too large", func() { c.body.Store(`{"client_id":"` + strings.Repeat("a", cimdMaxBytes) + `"}`) },
			func() *fetcher { return testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")}) }},
		{"redirect", func() {
			c.extra = func(w http.ResponseWriter) {
				w.Header().Set("Location", "https://example.com/elsewhere.json")
				w.WriteHeader(http.StatusFound)
			}
		}, func() *fetcher { return testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")}) }},
	}
	for _, tc := range cases {
		c.extra = nil
		tc.setup()
		if _, err := tc.f().get(ctx, id); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	f := testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")})
	if _, err := f.get(ctx, "http://example.com/c.json"); err == nil {
		t.Error("http client_id accepted")
	}
}

func TestCIMDTimeout(t *testing.T) {
	c := newCIMDServer(t)
	id := c.clientID()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	c.extra = func(http.ResponseWriter) { <-block }
	f := testFetcher(c, allowAll, fakeResolver{addrs: addrs("127.0.0.1")})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := f.get(ctx, id); err == nil {
		t.Fatal("no timeout")
	}
}

func TestCacheTTL(t *testing.T) {
	for cc, want := range map[string]time.Duration{
		"": cimdDefaultTTL, "no-store": 0, "max-age=60": time.Minute, "public, max-age=999999": cimdMaxTTL, "max-age=x": cimdDefaultTTL,
	} {
		h := http.Header{}
		h.Set("Cache-Control", cc)
		if got := cacheTTL(h); got != want {
			t.Errorf("%q: %v", cc, got)
		}
	}
}

// --- protocol endpoints -----------------------------------------------------

type rig struct {
	t   *testing.T
	env *testenv.Env
	s   *Server
	srv *httptest.Server
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	env := testenv.New(t)
	s := New(env.Svc, o)
	mux := http.NewServeMux()
	for _, rt := range s.Routes() {
		mux.Handle(rt.Pattern, rt.Handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &rig{t: t, env: env, s: s, srv: srv}
}

// reply is the part of a response the tests look at (the body is read and closed).
type reply struct {
	StatusCode int
	Header     http.Header
}

func (r *rig) req(method, path, ctype, body string) (*reply, string) {
	r.t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, r.srv.URL+path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return &reply{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

const form = "application/x-www-form-urlencoded"

func TestMetadataEndpoints(t *testing.T) {
	r := newRig(t, Options{})
	resp, body := r.req(http.MethodGet, ServerMeta, "", "")
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	if resp.StatusCode != 200 || m["issuer"] != "https://api.pp.test" || m["registration_endpoint"] != "https://api.pp.test/oauth/register" {
		t.Fatalf("server meta: %s", body)
	}
	if resp, _ := r.req(http.MethodPost, ServerMeta, "", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal("POST metadata")
	}
	if resp, _ := r.req(http.MethodOptions, TokenPath, "", ""); resp.StatusCode != http.StatusNoContent ||
		resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("CORS preflight")
	}
	for _, p := range []string{ResourceMeta, ResourceMeta + "/mcp"} {
		_, body := r.req(http.MethodGet, p, "", "")
		if !strings.Contains(body, `"resource":"https://api.pp.test/mcp"`) {
			t.Fatalf("%s: %s", p, body)
		}
	}
}

func TestRegisterEndpoint(t *testing.T) {
	r := newRig(t, Options{})
	cases := map[string]string{
		`nope`: "invalid_client_metadata",
		`{"redirect_uris":["https://a.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`: "invalid_client_metadata",
		`{"redirect_uris":["https://a.example/cb"],"grant_types":["client_credentials"]}`:               "invalid_client_metadata",
		`{"redirect_uris":["http://a.example/cb"]}`:                                                     "invalid_redirect_uri",
	}
	for body, code := range cases {
		resp, out := r.req(http.MethodPost, RegisterPath, "application/json", body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, code) {
			t.Errorf("%s: %d %s", body, resp.StatusCode, out)
		}
	}
	resp, out := r.req(http.MethodPost, RegisterPath, "application/json",
		`{"redirect_uris":["https://a.example/cb"],"client_name":"A","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(out, `"token_endpoint_auth_method":"none"`) {
		t.Fatalf("register: %d %s", resp.StatusCode, out)
	}
	if resp, _ := r.req(http.MethodGet, RegisterPath, "", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal("GET register")
	}
	var last *reply
	for range service.RegistrationsPerIP {
		last, _ = r.req(http.MethodPost, RegisterPath, "application/json", `{"redirect_uris":["https://a.example/cb"]}`)
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d", last.StatusCode)
	}
}

func TestTokenAndRevokeEndpoints(t *testing.T) {
	r := newRig(t, Options{})
	cases := []struct {
		body   string
		status int
		code   string
	}{
		{"grant_type=authorization_code&code=x", http.StatusUnauthorized, "invalid_client"},
		{"grant_type=password&client_id=c", http.StatusBadRequest, "unsupported_grant_type"},
		{"grant_type=authorization_code&client_id=c", http.StatusBadRequest, "invalid_request"},
		{"grant_type=authorization_code&client_id=c&code=x&code_verifier=y", http.StatusBadRequest, "invalid_grant"},
		{"grant_type=refresh_token&client_id=c&refresh_token=dsr_x", http.StatusBadRequest, "invalid_grant"},
		{"grant_type=refresh_token&grant_type=refresh_token&client_id=c", http.StatusBadRequest, "repeated"},
		{"%zz", http.StatusBadRequest, "invalid_request"},
	}
	for _, tc := range cases {
		resp, out := r.req(http.MethodPost, TokenPath, form, tc.body)
		if resp.StatusCode != tc.status || !strings.Contains(out, tc.code) {
			t.Errorf("%s: %d %s", tc.body, resp.StatusCode, out)
		}
	}
	// Client ID from HTTP Basic is accepted.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, r.srv.URL+TokenPath,
		strings.NewReader("grant_type=refresh_token&refresh_token=dsr_x"))
	req.Header.Set("Content-Type", form)
	req.SetBasicAuth("c", "")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("basic: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if resp, _ := r.req(http.MethodGet, TokenPath, "", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal("GET token")
	}
	if resp, _ := r.req(http.MethodPost, RevokePath, form, "token=dsa_unknown"); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, _ := r.req(http.MethodGet, RevokePath, "", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal("GET revoke")
	}
	if resp, _ := r.req(http.MethodPost, RevokePath, form, "%zz"); resp.StatusCode != http.StatusBadRequest {
		t.Fatal("bad revoke body")
	}
}

func TestValidateAuthorizeAndSeal(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	c, err := r.env.Svc.RegisterOAuthClient(ctx, "App", []string{"https://a.example/cb"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	ch := service.PKCEChallenge(strings.Repeat("v", 43))
	base := url.Values{"client_id": {c.ID}, "response_type": {"code"}, "code_challenge": {ch},
		"code_challenge_method": {"S256"}, "state": {"s"}}
	p, err := r.s.ValidateAuthorize(ctx, base)
	if err != nil || p.Request.RedirectURI != "https://a.example/cb" || p.Request.Resource != "https://api.pp.test/mcp" {
		t.Fatalf("valid: %+v %v", p, err)
	}
	var pe *PageError
	for _, q := range []url.Values{
		{"client_id": {"unknown"}},
		{"client_id": {c.ID}, "redirect_uri": {"https://evil.example/cb"}},
		{"client_id": {c.ID, c.ID}},
		{"client_id": {strings.Repeat("x", 100)}},
	} {
		if _, err := r.s.ValidateAuthorize(ctx, q); !errors.As(err, &pe) || pe.Error() == "" {
			t.Errorf("%v: %v", q, err)
		}
	}
	var re *RedirectError
	for k, v := range map[string]string{"response_type": "token", "code_challenge_method": "plain",
		"resource": "https://evil.example", "state": strings.Repeat("s", 2000)} {
		q := url.Values{}
		for kk, vv := range base {
			q[kk] = vv
		}
		q.Set(k, v)
		if _, err := r.s.ValidateAuthorize(ctx, q); !errors.As(err, &re) || re.Error() == "" {
			t.Errorf("%s=%s: %v", k, v, err)
		}
	}
	u, _ := url.Parse(r.s.ErrorURL(&RedirectError{RedirectURI: "https://a.example/cb?x=1", Code: "access_denied", State: "s"}))
	if u.Query().Get("x") != "1" || u.Query().Get("iss") != "https://api.pp.test" || u.Query().Get("error") != "access_denied" {
		t.Fatalf("error url: %s", u)
	}
	if got := withQuery("%zz", url.Values{"a": {"b"}}); got != "%zz" {
		t.Fatalf("bad base: %s", got)
	}

	sealed := r.s.Seal(p)
	back, err := r.s.Open(sealed)
	if err != nil || back.Request.ClientID != c.ID || back.State != "s" {
		t.Fatalf("open: %+v %v", back, err)
	}
	for _, bad := range []string{"", "x", sealed + "x", "e30." + r.s.sign("e30"), "!!." + r.s.sign("!!")} {
		if _, err := r.s.Open(bad); !errors.Is(err, ErrExpiredForm) {
			t.Errorf("open %q: %v", bad, err)
		}
	}
	r.s.nowFn = func() time.Time { return time.Now().Add(sealTTL + time.Minute) }
	if _, err := r.s.Open(sealed); !errors.Is(err, ErrExpiredForm) {
		t.Fatal("expired seal accepted")
	}
	if !strings.Contains(r.s.SuccessURL(p, "abc"), "code=abc") {
		t.Fatal("success url")
	}
}

func TestResolveMetadataClient(t *testing.T) {
	c := newCIMDServer(t)
	id := c.clientID()
	c.doc(id, "https://chat.example/cb")
	pool := x509.NewCertPool()
	pool.AddCert(c.srv.Certificate())
	r := newRig(t, Options{
		Resolver: fakeResolver{addrs: addrs("127.0.0.1")}, AllowAddr: allowAll,
		TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	})
	got, err := r.s.ResolveClient(context.Background(), id)
	if err != nil || !got.Metadata {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if r.s.Issuer() != "https://api.pp.test" {
		t.Fatal("issuer")
	}
	_ = fmt.Sprint
}
