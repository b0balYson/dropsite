package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/api"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/mcp/remote"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// The local stdio server runs the tools through the real API; these tests
// compare it with the remote /mcp server on the same service.

type hookSender struct {
	mu    sync.Mutex
	hooks int
}

func (s *hookSender) TelegramMe(context.Context, string) (string, error) {
	return "", forms.Permanent("no telegram in tests")
}
func (s *hookSender) TelegramSend(context.Context, string, string, string) error { return nil }
func (s *hookSender) TelegramUpdates(context.Context, string, int64) ([]forms.Update, error) {
	return nil, nil
}
func (s *hookSender) Email(context.Context, forms.SMTPConfig, string, string, string) error {
	return nil
}
func (s *hookSender) Webhook(context.Context, string, string, []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks++
	return nil
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// parityRig serves the API and the remote MCP endpoint of one service.
type parityRig struct {
	t      *testing.T
	env    *testenv.Env
	api    *httptest.Server
	remote *httptest.Server
	hooks  *hookSender
}

func newParityRig(t *testing.T) *parityRig {
	t.Helper()
	hooks := &hookSender{}
	env := testenv.New(t, testenv.WithFormSender(hooks))
	r := &parityRig{t: t, env: env, hooks: hooks}
	r.api = httptest.NewServer(api.New(env.Svc, nil))
	t.Cleanup(r.api.Close)
	r.remote = httptest.NewServer(remote.New(env.Svc, remote.Options{}))
	t.Cleanup(r.remote.Close)
	return r
}

// stdio connects the local MCP server of the CLI logged in with token.
func (r *parityRig) stdio(token string) *sdk.ClientSession {
	r.t.Helper()
	e := newTestEnv(r.t)
	e.env[envServer] = r.api.URL
	e.env[envToken] = token
	return mcpClient(r.t, e.app())
}

// web connects the remote MCP endpoint with token.
func (r *parityRig) web(token string) *sdk.ClientSession {
	r.t.Helper()
	tr := &sdk.StreamableClientTransport{
		Endpoint: r.remote.URL, HTTPClient: &http.Client{Transport: bearerRT{token}}, DisableStandaloneSSE: true,
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolSchemas(t *testing.T, cs *sdk.ClientSession) map[string]string {
	t.Helper()
	l, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tl := range l.Tools {
		b, err := json.Marshal(map[string]any{"in": tl.InputSchema, "out": tl.OutputSchema, "ann": tl.Annotations,
			"desc": tl.Description})
		if err != nil {
			t.Fatal(err)
		}
		out[tl.Name] = string(b)
	}
	return out
}

func TestStdioToolsMatchRemote(t *testing.T) {
	r := newParityRig(t)
	tok := r.env.Token(r.env.User("ann@pp.test"), "cli")
	local, web := toolSchemas(t, r.stdio(tok)), toolSchemas(t, r.web(tok))
	if _, ok := local["deploy_folder"]; !ok {
		t.Fatal("stdio has no deploy_folder")
	}
	if _, ok := web["deploy_files"]; !ok {
		t.Fatal("remote has no deploy_files")
	}
	delete(local, "deploy_folder")
	delete(web, "deploy_files")
	var names []string
	for name := range web {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(local) != len(web) {
		t.Errorf("stdio has %d tools, remote %d", len(local), len(web))
	}
	for _, name := range names {
		if local[name] == "" {
			t.Errorf("stdio lacks %s", name)
			continue
		}
		if local[name] != web[name] {
			t.Errorf("%s differs:\nstdio  %s\nremote %s", name, local[name], web[name])
		}
	}
	for name := range local {
		if web[name] == "" {
			t.Errorf("stdio has extra tool %s", name)
		}
	}
	for _, minted := range []string{"create_token", "create_connect_code", "connect"} {
		if local[minted] != "" {
			t.Errorf("stdio mints credentials with %s", minted)
		}
	}
}

func outputs(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	return callTool(t, cs, name, args)
}

func mustTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	out, msg := outputs(t, cs, name, args)
	if msg != "" {
		t.Fatalf("%s: %s", name, msg)
	}
	return out
}

func wantToolErr(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, sub string) {
	t.Helper()
	if _, msg := outputs(t, cs, name, args); !strings.Contains(msg, sub) {
		t.Fatalf("%s: error %q, want %q", name, msg, sub)
	}
}

// same calls a read-only tool on both servers and compares the outputs.
func same(t *testing.T, local, web *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	a, msgA := outputs(t, local, name, args)
	b, msgB := outputs(t, web, name, args)
	if msgA != msgB {
		t.Fatalf("%s errors differ:\nstdio  %q\nremote %q", name, msgA, msgB)
	}
	if !reflect.DeepEqual(a, b) {
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		t.Fatalf("%s outputs differ:\nstdio  %s\nremote %s", name, ja, jb)
	}
	return a
}

func TestStdioCabinetTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	other := r.env.Token(ann, "laptop")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Coffee", "coffee")
	if err != nil {
		t.Fatal(err)
	}

	acc := same(t, local, web, "get_account", map[string]any{})
	if acc["email"] != "ann@pp.test" || acc["is_admin"] != false || acc["sites_used"] != float64(1) ||
		!strings.Contains(acc["summary"].(string), "1 of") {
		t.Fatalf("account: %v", acc)
	}
	out := mustTool(t, local, "update_site", map[string]any{"site": "coffee", "title": "Coffee & Co"})
	if out["title"] != "Coffee & Co" {
		t.Fatalf("update_site: %v", out)
	}
	wantToolErr(t, local, "update_site", map[string]any{"site": "coffee"}, "nothing to change")

	out = mustTool(t, local, "set_site_access", map[string]any{"site": "coffee", "password": "s3cret-pass-1",
		"hidden_from_search": true, "expires_on": "2030-01-31"})
	if out["password_protected"] != true || out["hidden_from_search"] != true || out["last_day"] != "2030-01-31" ||
		!strings.Contains(out["summary"].(string), "password") {
		t.Fatalf("access: %v", out)
	}
	same(t, local, web, "set_site_access", map[string]any{"site": "coffee"})
	out = mustTool(t, local, "set_site_access", map[string]any{"site": "coffee", "remove_password": true, "remove_expiry": true})
	if out["password_protected"] != false || out["expires_at"] != nil {
		t.Fatalf("access removed: %v", out)
	}
	same(t, local, web, "get_site", map[string]any{"site": "coffee"})
	same(t, local, web, "list_sites", map[string]any{})

	added := mustTool(t, local, "add_domain", map[string]any{"site": "coffee", "domain": "shop.example.org"})
	if added["site_id"] != v.Site.ID || !strings.Contains(added["summary"].(string), "approve") {
		t.Fatalf("add_domain: %v", added)
	}
	doms := same(t, local, web, "list_domains", map[string]any{"site": "coffee"})
	if len(doms["domains"].([]any)) != 1 {
		t.Fatalf("domains: %v", doms)
	}
	same(t, local, web, "list_sites", map[string]any{})
	wantToolErr(t, local, "remove_domain", map[string]any{"site": "coffee", "domain": "shop.example.org", "confirm": "x"}, "not removed")
	wantToolErr(t, local, "remove_domain", map[string]any{"site": "coffee", "domain": "nope.example.org",
		"confirm": "nope.example.org"}, "list_domains")
	mustTool(t, local, "remove_domain", map[string]any{"site": "coffee", "domain": "shop.example.org", "confirm": "shop.example.org"})

	same(t, local, web, "get_site_stats", map[string]any{"site": "coffee", "days": 30})
	same(t, local, web, "get_site_stats", map[string]any{"site": "coffee", "days": 1000})

	out = mustTool(t, local, "set_counters", map[string]any{"site": "coffee", "yandex_metrica_id": "12345"})
	if out["own"].(map[string]any)["yandex_metrica_id"] != "12345" || !strings.HasPrefix(out["summary"].(string), "Saved.") {
		t.Fatalf("counters: %v", out)
	}
	same(t, local, web, "get_counters", map[string]any{"site": "coffee"})
	same(t, local, web, "set_counters", map[string]any{"site": "coffee", "yandex_metrica_id": "bad id"})

	conns := same(t, local, web, "list_connections", map[string]any{})
	var laptopID float64
	for _, c := range conns["connections"].([]any) {
		m := c.(map[string]any)
		if m["name"] == "laptop" {
			laptopID = m["id"].(float64)
		}
		if m["name"] == "cli" && m["current"] != true {
			t.Fatalf("current: %v", conns)
		}
	}
	wantToolErr(t, local, "revoke_connection", map[string]any{"kind": "token", "id": laptopID, "confirm": "x"}, "not revoked")
	wantToolErr(t, local, "revoke_connection", map[string]any{"kind": "app", "id": laptopID, "confirm": "laptop"}, "not found")
	mustTool(t, local, "revoke_connection", map[string]any{"kind": "token", "id": laptopID, "confirm": "laptop"})
	if _, err := r.env.Svc.AuthenticateToken(ctx, other, "198.51.100.1"); err == nil {
		t.Fatal("revoked token still works")
	}

	// A regular user cannot touch the root domain, through either server.
	same(t, local, web, "set_root_site", map[string]any{"site": "coffee", "confirm": "coffee"})
	same(t, local, web, "clear_root_site", map[string]any{"confirm": "coffee"})

	// Another user's site does not exist for this user.
	bob := r.env.User("bob@pp.test")
	bv, err := r.env.Svc.CreateSite(ctx, bob, "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{
		"update_site":     {"site": bv.Site.ID, "title": "x"},
		"set_site_access": {"site": bv.Site.ID, "hidden_from_search": true},
		"list_domains":    {"site": bv.Site.ID},
		"add_domain":      {"site": bv.Site.ID, "domain": "b.example.org"},
		"get_site_stats":  {"site": bv.Site.ID},
		"get_counters":    {"site": bv.Site.ID},
		"set_counters":    {"site": bv.Site.ID, "yandex_metrica_id": "1"},
	} {
		same(t, local, web, name, args)
		wantToolErr(t, local, name, args, "not found")
	}
}

func TestStdioRootSiteNeedsConfirm(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	tok := r.env.Token(r.env.Admin, "cli")
	local, web := r.stdio(tok), r.web(tok)
	bob := r.env.User("bob@pp.test")
	if _, err := r.env.Svc.CreateSite(ctx, bob, "Placeholder", "soon"); err != nil {
		t.Fatal(err)
	}
	// Apart from the root domain, an administrator's assistant has no
	// administrator powers on either server.
	wantToolErr(t, local, "get_site", map[string]any{"site": "soon"}, "not found")
	same(t, local, web, "get_site", map[string]any{"site": "soon"})
	same(t, local, web, "set_root_site", map[string]any{"site": "soon", "confirm": "other"})
	out :=mustTool(t, local, "set_root_site", map[string]any{"site": "soon", "confirm": "soon"})
	if !strings.Contains(out["summary"].(string), "Placeholder") {
		t.Fatalf("set root: %v", out)
	}
	acc := same(t, local, web, "get_account", map[string]any{})
	if acc["is_admin"] != true || acc["root_site"] == nil || !strings.Contains(acc["summary"].(string), "Placeholder") {
		t.Fatalf("admin account: %v", acc)
	}
	same(t, local, web, "clear_root_site", map[string]any{"confirm": "nope"})
	out = mustTool(t, local, "clear_root_site", map[string]any{"confirm": "soon"})
	if !strings.Contains(out["summary"].(string), "no longer shows") {
		t.Fatalf("clear root: %v", out)
	}
}

func submitForm(t *testing.T, env *testenv.Env, siteID, form string, i int, fields ...string) {
	t.Helper()
	p := service.FormPost{Host: siteID + "." + env.Config.BaseDomain, Form: form, IP: fmt.Sprintf("198.51.100.%d", i)}
	for j := 0; j+1 < len(fields); j += 2 {
		p.Data.Fields = append(p.Data.Fields, domain.FormField{Name: fields[j], Value: fields[j+1]})
	}
	if _, err := env.Svc.SubmitForm(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestStdioFormsTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe")
	if err != nil {
		t.Fatal(err)
	}
	st := same(t, local, web, "get_form_settings", map[string]any{"site": "cafe"})
	if !strings.Contains(st["form_endpoint"].(string), "/_form/<name>") || !strings.Contains(st["form_snippet"].(string), "altcha") {
		t.Fatalf("settings: %v", st)
	}
	out := mustTool(t, local, "set_form_settings", map[string]any{"site": "cafe", "require_altcha": true})
	if out["require_altcha"] != true {
		t.Fatalf("set: %v", out)
	}
	mustTool(t, local, "set_form_settings", map[string]any{"site": "cafe", "require_altcha": false})

	hook := mustTool(t, local, "add_channel", map[string]any{"site": "cafe", "kind": "webhook", "target": "https://hooks.example.com/in"})
	if hook["secret"] == "" || !strings.Contains(hook["next_step"].(string), "only this once") {
		t.Fatalf("webhook: %v", hook)
	}
	id := hook["channel"].(map[string]any)["id"].(string)
	same(t, local, web, "add_channel", map[string]any{"site": "cafe", "kind": "telegram"})
	submitForm(t, r.env, v.Site.ID, "lead", 1, "name", "Anna", "phone", "=1+1")
	submitForm(t, r.env, v.Site.ID, "order", 2, "name", "Boris")
	submitForm(t, r.env, v.Site.ID, "lead", 3, "name", "Clara")
	for range 3 {
		if _, err := r.env.Svc.DeliverDueForms(ctx); err != nil {
			t.Fatal(err)
		}
	}
	same(t, local, web, "list_channels", map[string]any{"site": "cafe"})
	page := same(t, local, web, "list_submissions", map[string]any{"site": "cafe", "limit": 2})
	if page["total"] != float64(3) || page["next_cursor"] == "" {
		t.Fatalf("page: %v", page)
	}
	same(t, local, web, "list_submissions", map[string]any{"site": "cafe", "limit": 2, "cursor": page["next_cursor"]})
	same(t, local, web, "list_submissions", map[string]any{"site": "cafe", "days": 1, "form": "LEAD"})
	same(t, local, web, "list_submissions", map[string]any{"site": "cafe", "since": "2020-01-01", "until": "2099-12-31"})
	same(t, local, web, "list_submissions", map[string]any{"site": "cafe", "cursor": "zzz"})
	same(t, local, web, "export_submissions", map[string]any{"site": "cafe", "max_rows": 1})
	same(t, local, web, "export_submissions", map[string]any{"site": "cafe"})

	wantToolErr(t, local, "verify_channel", map[string]any{"site": "cafe", "channel_id": "../../x", "code": "123456"}, "not found")
	out = mustTool(t, local, "set_channel_enabled", map[string]any{"site": "cafe", "channel_id": id, "enabled": false})
	if out["state"] != "off" {
		t.Fatalf("off: %v", out)
	}
	wantToolErr(t, local, "rotate_channel_secret", map[string]any{"site": "cafe", "channel_id": id, "confirm": "x"}, "nothing changed")
	out = mustTool(t, local, "rotate_channel_secret", map[string]any{"site": "cafe", "channel_id": id, "confirm": id})
	if out["secret"] == "" || out["secret"] == hook["secret"] {
		t.Fatalf("rotate: %v", out)
	}
	mustTool(t, local, "delete_channel", map[string]any{"site": "cafe", "channel_id": id, "confirm": "https://hooks.example.com/in"})

	bob := r.env.Token(r.env.User("bob@pp.test"), "cli")
	bl, bw := r.stdio(bob), r.web(bob)
	for name, args := range map[string]map[string]any{
		"get_form_settings":  {"site": v.Site.ID},
		"set_form_settings":  {"site": v.Site.ID, "require_altcha": true},
		"list_submissions":   {"site": v.Site.ID},
		"export_submissions": {"site": v.Site.ID},
		"list_channels":      {"site": v.Site.ID},
		"add_channel":        {"site": v.Site.ID, "kind": "webhook", "target": "https://hooks.example.com/b"},
		"verify_channel":     {"site": v.Site.ID, "channel_id": id, "code": "123456"},
	} {
		same(t, bl, bw, name, args)
		wantToolErr(t, bl, name, args, "not found")
	}
}

func TestStdioSiteFilesTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Shop", "shop")
	if err != nil {
		t.Fatal(err)
	}
	same(t, local, web, "list_site_files", map[string]any{"site": "shop"})
	dir := siteDir(t, map[string]string{"index.html": "<h1>Price 10 <b>&</b></h1>", "css/site.css": "body{}",
		"img/logo.png": "\x89PNG\r\n\x1a\n\x00"})
	mustTool(t, local, "deploy_folder", map[string]any{"path": dir, "site": "shop"})

	list := same(t, local, web, "list_site_files", map[string]any{"site": "shop"})
	same(t, local, web, "list_site_files", map[string]any{"site": "shop", "prefix": "css/"})
	read := same(t, local, web, "read_site_files", map[string]any{"site": "shop", "paths": []string{"index.html", "img/*", "nope.txt"}})
	if len(read["files"].([]any)) != 2 || len(read["missing"].([]any)) != 1 {
		t.Fatalf("read: %v", read)
	}
	same(t, local, web, "read_site_files", map[string]any{"site": "shop", "paths": []string{}})
	base := list["deploy_id"].(string)

	upd := map[string]any{"site": "shop", "base_deploy_id": base,
		"files":  []map[string]string{{"path": "index.html", "content": "<h1>Price 12 <b>&</b></h1>"}},
		"delete": []string{"css/"}}
	out := mustTool(t, local, "update_site_files", upd)
	if out["written"] != float64(1) || out["deleted"] != float64(1) || out["file_count"] != float64(2) ||
		!strings.HasPrefix(out["summary"].(string), "Site updated: 1 file(s) written, 1 removed") {
		t.Fatalf("update: %v", out)
	}
	if got := mustTool(t, web, "read_site_files", map[string]any{"site": "shop", "paths": []string{"index.html"}}); got["files"].([]any)[0].(map[string]any)["content"] != "<h1>Price 12 <b>&</b></h1>" {
		t.Fatalf("content: %v", got)
	}
	same(t, local, web, "update_site_files", upd) // stale base: the same conflict

	up := mustTool(t, local, "begin_upload", map[string]any{"title": "Big"})
	if up["upload_id"] == "" || up["mode"] != "replace" {
		t.Fatalf("begin: %v", up)
	}
	same(t, local, web, "upload_files", map[string]any{"upload_id": "up_missing", "files": []map[string]string{{"path": "a.txt", "content": "a"}}})
	// Sessions belong to the user, not to the connection.
	mustTool(t, web, "upload_files", map[string]any{"upload_id": up["upload_id"],
		"files": []map[string]string{{"path": "index.html", "content": "<p>big</p>"}}})
	up = mustTool(t, local, "upload_files", map[string]any{"upload_id": up["upload_id"],
		"files": []map[string]string{{"path": "a.css", "content": "p{}"}}})
	if up["files"] != float64(2) {
		t.Fatalf("upload: %v", up)
	}
	out = mustTool(t, local, "commit_upload", map[string]any{"upload_id": up["upload_id"]})
	if out["file_count"] != float64(2) || !strings.HasPrefix(out["summary"].(string), "Site published") || out["site_id"] == v.Site.ID {
		t.Fatalf("commit: %v", out)
	}

	bob := r.env.Token(r.env.User("bob@pp.test"), "cli")
	bl, bw := r.stdio(bob), r.web(bob)
	for name, args := range map[string]map[string]any{
		"list_site_files":   {"site": v.Site.ID},
		"read_site_files":   {"site": v.Site.ID, "paths": []string{"index.html"}},
		"update_site_files": {"site": v.Site.ID, "base_deploy_id": base, "delete": []string{"index.html"}},
		"begin_upload":      {"site": v.Site.ID, "mode": "patch", "base_deploy_id": base},
	} {
		same(t, bl, bw, name, args)
		wantToolErr(t, bl, name, args, "not found")
	}
	same(t, bl, bw, "commit_upload", map[string]any{"upload_id": up["upload_id"]})
}
