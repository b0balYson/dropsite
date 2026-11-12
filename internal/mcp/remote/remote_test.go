package remote

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

type rig struct {
	t    *testing.T
	env  *testenv.Env
	srv  *httptest.Server
	user int64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	env := testenv.New(t)
	r := &rig{t: t, env: env}
	h := New(env.Svc, Options{OnAuth: func(_ context.Context, id int64) { r.user = id }})
	r.srv = httptest.NewServer(h)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rig) session(token string) *sdk.ClientSession {
	r.t.Helper()
	tr := &sdk.StreamableClientTransport{
		Endpoint: r.srv.URL, HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true,
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return nil, res.Content[0].(*sdk.TextContent).Text
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	return out, ""
}

func TestRemoteToolsWithAPIToken(t *testing.T) {
	r := newRig(t)
	alice := r.env.User("alice@pp.test")
	cs := r.session(r.env.Token(alice, "mcp"))
	if !strings.Contains(cs.InitializeResult().Instructions, "deploy_files") {
		t.Fatal("no instructions")
	}

	out, msg := call(t, cs, "deploy_files", map[string]any{"title": "Promo",
		"files": []map[string]string{{"path": "index.html", "content": "v1"}}})
	if msg != "" {
		t.Fatal(msg)
	}
	if r.user != alice.User.ID {
		t.Fatal("OnAuth not called")
	}
	site := out["site_id"].(string)
	r.env.Clock.Advance(time.Second)
	if _, msg := call(t, cs, "deploy_files", map[string]any{"site": site,
		"files": []map[string]string{{"path": "index.html", "content": "v2"}}}); msg != "" {
		t.Fatal(msg)
	}
	out, _ = call(t, cs, "list_sites", map[string]any{})
	if len(out["sites"].([]any)) != 1 {
		t.Fatalf("list: %v", out)
	}
	out, _ = call(t, cs, "get_site", map[string]any{"site": site})
	if out["current_deploy"] == nil {
		t.Fatalf("get: %v", out)
	}
	out, _ = call(t, cs, "set_slug", map[string]any{"site": site, "slug": "promo-may"})
	if out["slug"] != "promo-may" || !strings.Contains(out["url"].(string), "promo-may.") {
		t.Fatalf("slug: %v", out)
	}
	if _, msg := call(t, cs, "set_slug", map[string]any{"site": site, "slug": "www"}); !strings.Contains(msg, "slug_reserved") {
		t.Fatalf("reserved slug: %q", msg)
	}
	out, _ = call(t, cs, "list_deploys", map[string]any{"site": "promo-may"})
	ds := out["deploys"].([]any)
	if len(ds) != 2 || ds[0].(map[string]any)["current"] != true || ds[0].(map[string]any)["source"] != "mcp" {
		t.Fatalf("deploys: %v", out)
	}
	out, _ = call(t, cs, "rollback", map[string]any{"site": site})
	if out["deploy_id"] != ds[1].(map[string]any)["id"] {
		t.Fatalf("rollback: %v", out)
	}
	if _, msg := call(t, cs, "get_site", map[string]any{"site": "nope"}); !strings.Contains(msg, "list_sites") {
		t.Fatalf("missing: %q", msg)
	}
	if _, msg := call(t, cs, "deploy_files", map[string]any{"files": []map[string]string{
		{"path": "index.html", "content": strings.Repeat("a", service.MaxInlineBytes+1)}}}); !strings.Contains(msg, "dropsite deploy") {
		t.Fatalf("too large: %q", msg)
	}
	if _, msg := call(t, cs, "delete_site", map[string]any{"site": site, "confirm": "x"}); msg == "" {
		t.Fatal("unconfirmed delete")
	}
	out, msg = call(t, cs, "delete_site", map[string]any{"site": site, "confirm": "promo-may"})
	if msg != "" || out["deleted"] != site {
		t.Fatalf("delete: %v %q", out, msg)
	}
	out, _ = call(t, cs, "create_site", map[string]any{"title": "Empty"})
	if out["url"] == "" {
		t.Fatalf("create: %v", out)
	}
	// Another user's site is not found.
	bob := r.env.User("bob@pp.test")
	bs := r.session(r.env.Token(bob, "mcp"))
	if _, msg := call(t, bs, "get_site", map[string]any{"site": out["id"].(string)}); !strings.Contains(msg, "not found") {
		t.Fatalf("foreign: %q", msg)
	}
}

func TestRemoteAuthErrors(t *testing.T) {
	r := newRig(t)
	post := func(token string) *http.Response {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, r.srv.URL,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	resp := post("")
	wa := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(wa, `resource_metadata="https://api.pp.test/.well-known/oauth-protected-resource/mcp"`) ||
		!strings.Contains(wa, `scope="sites"`) {
		t.Fatalf("no token: %d %q", resp.StatusCode, wa)
	}
	var last *http.Response
	for range service.FailedTokensPerIP + 1 {
		last = post("ds_wrong")
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" || last.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("rate limit: %d %v", last.StatusCode, last.Header)
	}
	r.env.Clock.Advance(service.FailedLoginsWindow + time.Second)
	if post("dsa_wrong").StatusCode != http.StatusUnauthorized {
		t.Fatal("oauth garbage accepted")
	}
}

func TestResolverAndErrors(t *testing.T) {
	env := testenv.New(t)
	res := resolver(env.Svc, slog.New(slog.DiscardHandler))
	for _, req := range []*sdk.CallToolRequest{nil, {}, {Extra: &sdk.RequestExtra{}}} {
		if _, err := res(context.Background(), req); !errors.Is(err, errNoActor) {
			t.Fatalf("resolver: %v", err)
		}
	}
	b := backend{svc: env.Svc, log: slog.New(slog.DiscardHandler)}
	for in, want := range map[error]string{
		errors.New("disk on fire"):                                    "internal server error",
		domain.ErrRateLimited.WithRetryAfter(1500 * time.Millisecond): "retry in 2 seconds",
		domain.ErrUnauthorized:                                        "reconnect",
	} {
		if got := b.toolError(in).Error(); !strings.Contains(got, want) || strings.Contains(got, "fire") {
			t.Errorf("%v → %q", in, got)
		}
	}
	if b.toolError(nil) != nil {
		t.Fatal("nil error")
	}
	w := &rateWriter{ResponseWriter: httptest.NewRecorder(), hit: &rateHit{}}
	w.Flush()
	if w.Unwrap() == nil {
		t.Fatal("unwrap")
	}
}
