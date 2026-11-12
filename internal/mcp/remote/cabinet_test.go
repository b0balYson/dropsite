package remote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

const pkceVerifier = "verifier-0123456789-0123456789-0123456789-abcdef"

// oauthToken connects a chat app for a through the real OAuth flow and
// returns its access token.
func (r *rig) oauthToken(a service.Actor, client string) string {
	r.t.Helper()
	ctx := context.Background()
	// The bearer middleware checks the expiry against the real clock.
	r.env.Clock.Set(time.Now())
	code, err := r.env.Svc.CreateConnectCode(ctx, a)
	if err != nil {
		r.t.Fatal(err)
	}
	authCode, err := r.env.Svc.AuthorizeOAuth(ctx, service.AuthorizeRequest{
		ClientID: client, ClientName: "Chat App", RedirectURI: "https://chat.example/cb",
		Challenge: service.PKCEChallenge(pkceVerifier), Resource: r.env.Svc.MCPResource(),
	}, code.Code, "1.1.1.1")
	if err != nil {
		r.t.Fatal(err)
	}
	tok, err := r.env.Svc.ExchangeOAuthCode(ctx, authCode, pkceVerifier, client, "https://chat.example/cb", "")
	if err != nil {
		r.t.Fatal(err)
	}
	return tok.AccessToken
}

func (r *rig) auditSources(action string) []any {
	r.t.Helper()
	rows, _, err := r.env.Svc.ListAudit(context.Background(), r.env.Admin, service.AuditFilter{Action: action, Page: service.Page{Limit: 100}})
	if err != nil {
		r.t.Fatal(err)
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Meta["source"])
	}
	return out
}

func mustCall(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	out, msg := call(t, cs, name, args)
	if msg != "" {
		t.Fatalf("%s: %s", name, msg)
	}
	return out
}

func wantErr(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, substr string) {
	t.Helper()
	if _, msg := call(t, cs, name, args); msg == "" || !strings.Contains(msg, substr) {
		t.Fatalf("%s %v: want error with %q, got %q", name, args, substr, msg)
	}
}

func TestCabinetSettingsTools(t *testing.T) {
	r := newRig(t)
	alice := r.env.User("alice@pp.test")
	cs := r.session(r.env.Token(alice, "assistant"))

	out := mustCall(t, cs, "deploy_files", map[string]any{"title": "Coffee",
		"files": []map[string]string{{"path": "index.html", "content": "<p>coffee</p>"}}})
	site := out["site_id"].(string)

	acc := mustCall(t, cs, "get_account", map[string]any{})
	if acc["email"] != "alice@pp.test" || acc["is_admin"] != false || acc["sites_used"] != float64(1) ||
		acc["root_site"] != nil || !strings.Contains(acc["summary"].(string), "1 of") || acc["max_site_size"] == "" {
		t.Fatalf("account: %v", acc)
	}

	out = mustCall(t, cs, "update_site", map[string]any{"site": site, "title": "Coffee shop", "slug": "coffee"})
	if out["title"] != "Coffee shop" || !strings.Contains(out["url"].(string), "coffee.") {
		t.Fatalf("update: %v", out)
	}
	out = mustCall(t, cs, "update_site", map[string]any{"site": "coffee", "title": "Coffee & tea"})
	if out["slug"] != "coffee" {
		t.Fatalf("title-only update changed the slug: %v", out)
	}
	wantErr(t, cs, "update_site", map[string]any{"site": site, "slug": "www"}, "slug_reserved")

	out = mustCall(t, cs, "set_site_access", map[string]any{"site": "coffee"})
	if out["password_protected"] != false || out["last_day"] != nil {
		t.Fatalf("current access: %v", out)
	}
	lastDay := r.env.Clock.Now().AddDate(0, 0, 10).Format(time.DateOnly)
	out = mustCall(t, cs, "set_site_access", map[string]any{"site": "coffee", "password": "visitor-pass-1",
		"hidden_from_search": true, "expires_on": lastDay})
	raw, _ := json.Marshal(out)
	if out["password_protected"] != true || out["hidden_from_search"] != true || out["last_day"] != lastDay ||
		strings.Contains(string(raw), "visitor-pass-1") {
		t.Fatalf("access: %s", raw)
	}
	wantErr(t, cs, "set_site_access", map[string]any{"site": "coffee", "password": "x"}, "password_too_short")
	wantErr(t, cs, "set_site_access", map[string]any{"site": "coffee", "expires_on": "2020-01-01"}, "expiry_invalid")

	out = mustCall(t, cs, "get_site", map[string]any{"site": "coffee"})
	if out["password_protected"] != true || out["hidden_from_search"] != true || out["expires_at"] == nil {
		t.Fatalf("get_site extras: %v", out)
	}
	out = mustCall(t, cs, "set_site_access", map[string]any{"site": "coffee", "remove_password": true, "remove_expiry": true,
		"hidden_from_search": false})
	if out["password_protected"] != false || out["hidden_from_search"] != false || out["expires_at"] != nil {
		t.Fatalf("access removed: %v", out)
	}
	if srcs := r.auditSources("site.protect"); len(srcs) != 1 || srcs[0] != "mcp" {
		t.Fatalf("audit sources: %v", srcs)
	}

	out = mustCall(t, cs, "add_domain", map[string]any{"site": "coffee", "domain": "Shop.Example.com"})
	dom := out["domain"].(map[string]any)
	dns := out["dns"].(map[string]any)
	sub := dns["for_subdomain"].([]any)[0].(map[string]any)
	if dom["domain"] != "shop.example.com" || dom["status"] != "pending" || sub["type"] != "CNAME" ||
		sub["value"] != site+"."+testenv.BaseDomain || dns["server_ips_known"] != true ||
		!strings.Contains(out["summary"].(string), "administrator") {
		t.Fatalf("add domain: %v", out)
	}
	wantErr(t, cs, "add_domain", map[string]any{"site": "coffee", "domain": "x." + testenv.BaseDomain}, "domain_forbidden")
	out = mustCall(t, cs, "list_domains", map[string]any{"site": "coffee"})
	if len(out["domains"].([]any)) != 1 || !strings.Contains(out["summary"].(string), "waits") {
		t.Fatalf("list domains: %v", out)
	}
	out = mustCall(t, cs, "list_sites", map[string]any{})
	row := out["sites"].([]any)[0].(map[string]any)
	if od := row["own_domains"].([]any); len(od) != 1 || od[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("list_sites extras: %v", row)
	}
	wantErr(t, cs, "remove_domain", map[string]any{"site": "coffee", "domain": "shop.example.com", "confirm": "coffee"}, "confirm")
	wantErr(t, cs, "remove_domain", map[string]any{"site": "coffee", "domain": "nope.example.com", "confirm": "nope.example.com"}, "list_domains")
	mustCall(t, cs, "remove_domain", map[string]any{"site": "coffee", "domain": "shop.example.com", "confirm": "shop.example.com"})
	if out = mustCall(t, cs, "list_domains", map[string]any{"site": "coffee"}); len(out["domains"].([]any)) != 0 {
		t.Fatalf("domain not removed: %v", out)
	}

	out = mustCall(t, cs, "get_site_stats", map[string]any{"site": "coffee"})
	if len(out["daily"].([]any)) != 7 || out["to"] != r.env.Clock.Now().Format(time.DateOnly) ||
		!strings.Contains(out["summary"].(string), "No visits") {
		t.Fatalf("stats: %v", out)
	}
	wantErr(t, cs, "get_site_stats", map[string]any{"site": "coffee", "days": 1000}, "days")

	out = mustCall(t, cs, "set_counters", map[string]any{"site": "coffee", "yandex_metrica_id": "12345678"})
	if out["own"].(map[string]any)["yandex_metrica_id"] != "12345678" || out["new_deploy_id"] == nil {
		t.Fatalf("set counters: %v", out)
	}
	r.env.Clock.Advance(time.Second)
	out = mustCall(t, cs, "set_counters", map[string]any{"site": "coffee", "google_analytics_id": "G-ABC123"})
	own := out["own"].(map[string]any)
	if own["yandex_metrica_id"] != "12345678" || own["google_analytics_id"] != "G-ABC123" {
		t.Fatalf("partial counters update: %v", out)
	}
	wantErr(t, cs, "set_counters", map[string]any{"site": "coffee", "yandex_metrica_id": "abc"}, "counter_id_invalid")
	if out = mustCall(t, cs, "get_counters", map[string]any{"site": "coffee"}); !strings.Contains(out["summary"].(string), "12345678") {
		t.Fatalf("get counters: %v", out)
	}

	// Another user's site is not found by any cabinet tool.
	bob := r.env.User("bob@pp.test")
	bs := r.session(r.env.Token(bob, "bob"))
	for name, args := range map[string]map[string]any{
		"update_site":     {"site": site, "title": "mine"},
		"set_site_access": {"site": site, "remove_password": true},
		"list_domains":    {"site": site},
		"add_domain":      {"site": site, "domain": "evil.example.com"},
		"get_site_stats":  {"site": site},
		"get_counters":    {"site": site},
		"set_counters":    {"site": site, "yandex_metrica_id": ""},
	} {
		wantErr(t, bs, name, args, "not found")
	}
	// A regular user cannot touch the root domain, whatever the site.
	wantErr(t, cs, "set_root_site", map[string]any{"site": site, "confirm": site}, "only an administrator")
	wantErr(t, cs, "clear_root_site", map[string]any{"confirm": site}, "only an administrator")
}

func TestCabinetRootSiteForAdminAssistant(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	annCS := r.session(r.env.Token(ann, "a"))
	placeholder := mustCall(t, annCS, "deploy_files", map[string]any{"title": "Placeholder",
		"files": []map[string]string{{"path": "index.html", "content": "soon"}}})["site_id"].(string)
	other := mustCall(t, annCS, "deploy_files", map[string]any{"title": "Other",
		"files": []map[string]string{{"path": "index.html", "content": "other"}}})["site_id"].(string)

	admin := r.session(r.oauthToken(r.env.Admin, "admin-chat"))
	// The administrator's assistant has no other administrator powers.
	wantErr(t, admin, "get_site", map[string]any{"site": placeholder}, "not found")
	wantErr(t, admin, "update_site", map[string]any{"site": placeholder, "title": "x"}, "not found")
	wantErr(t, admin, "delete_site", map[string]any{"site": placeholder, "confirm": placeholder}, "not found")

	wantErr(t, admin, "set_root_site", map[string]any{"site": placeholder, "confirm": other}, "confirm")
	wantErr(t, admin, "set_root_site", map[string]any{"site": "missing-site", "confirm": "missing-site"}, "not found")
	if _, err := r.env.Served(testenv.BaseDomain, "index.html"); err == nil {
		t.Fatal("root changed without confirmation")
	}
	out := mustCall(t, admin, "set_root_site", map[string]any{"site": placeholder, "confirm": placeholder})
	if out["root_url"] != "https://"+testenv.BaseDomain+"/" || out["site"].(map[string]any)["root_domain"] != true {
		t.Fatalf("set root: %v", out)
	}
	for host, want := range map[string]string{testenv.BaseDomain: "soon", placeholder + "." + testenv.BaseDomain: "soon",
		other + "." + testenv.BaseDomain: "other"} {
		if body, _ := r.env.Served(host, "index.html"); body != want {
			t.Fatalf("%s serves %q, want %q", host, body, want)
		}
	}
	acc := mustCall(t, admin, "get_account", map[string]any{})
	if acc["is_admin"] != true || acc["root_site"].(map[string]any)["id"] != placeholder {
		t.Fatalf("admin account: %v", acc)
	}
	wantErr(t, admin, "clear_root_site", map[string]any{"confirm": other}, placeholder)
	out = mustCall(t, admin, "clear_root_site", map[string]any{"confirm": placeholder})
	if out["site"].(map[string]any)["root_domain"] == true {
		t.Fatalf("clear root: %v", out)
	}
	if _, err := r.env.Served(testenv.BaseDomain, "index.html"); err == nil {
		t.Fatal("root still served")
	}
	if body, _ := r.env.Served(placeholder+"."+testenv.BaseDomain, "index.html"); body != "soon" {
		t.Fatal("placeholder lost its own address")
	}
	for _, action := range []string{"site.apex.set", "site.apex.clear"} {
		if srcs := r.auditSources(action); len(srcs) != 1 || srcs[0] != "mcp" {
			t.Fatalf("%s sources %v", action, srcs)
		}
	}
	if v, err := r.env.Svc.GetSite(ctx, ann, placeholder); err != nil || v.OwnerID != ann.User.ID {
		t.Fatal("the site stays with its owner")
	}
}

// An OAuth-connected assistant cannot obtain a broader or longer-lived
// credential, and revoking its grant cuts its access at once.
func TestCabinetConnectionsAndCredentials(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	alice := r.env.User("alice@pp.test")
	access := r.oauthToken(alice, "chat")
	cs := r.session(access)
	cli := r.env.Token(alice, "laptop")
	bob := r.env.User("bob@pp.test")
	bobToken := r.env.Token(bob, "bob-cli")

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		n := tool.Name
		if strings.Contains(n, "token") || strings.Contains(n, "code") || strings.HasPrefix(n, "create_conn") ||
			strings.HasPrefix(n, "connect") {
			t.Fatalf("credential-issuing tool %s", n)
		}
	}
	// The OAuth access token is not an API token: it cannot open the HTTP API.
	if _, err := r.env.Svc.AuthenticateToken(ctx, access, "1.1.1.1"); err == nil {
		t.Fatal("OAuth access token accepted as an API token")
	}

	out := mustCall(t, cs, "list_connections", map[string]any{})
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), access) || strings.Contains(string(raw), cli) || strings.Contains(string(raw), cli[:12]) {
		t.Fatalf("secrets in list_connections: %s", raw)
	}
	conns := out["connections"].([]any)
	if len(conns) != 2 {
		t.Fatalf("connections: %s", raw)
	}
	var app, tok map[string]any
	for _, c := range conns {
		m := c.(map[string]any)
		if m["kind"] == "app" {
			app = m
		} else {
			tok = m
		}
	}
	if app["current"] != true || app["name"] != "Chat App" || tok["current"] != false || tok["name"] != "laptop" {
		t.Fatalf("connections: %s", raw)
	}

	// Bob's token is not Alice's to revoke.
	bobTokens, _ := r.env.Svc.ListTokens(ctx, bob)
	wantErr(t, cs, "revoke_connection", map[string]any{"kind": "token", "id": bobTokens[0].ID, "confirm": "bob-cli"}, "not found")
	if _, err := r.env.Svc.AuthenticateToken(ctx, bobToken, "1.1.1.1"); err != nil {
		t.Fatal("bob's token was revoked")
	}

	wantErr(t, cs, "revoke_connection", map[string]any{"kind": "token", "id": tok["id"], "confirm": "wrong"}, "confirm")
	mustCall(t, cs, "revoke_connection", map[string]any{"kind": "token", "id": tok["id"], "confirm": "laptop"})
	if _, err := r.env.Svc.AuthenticateToken(ctx, cli, "1.1.1.1"); err == nil {
		t.Fatal("revoked token still works")
	}

	out = mustCall(t, cs, "revoke_connection", map[string]any{"kind": "app", "id": app["id"], "confirm": "Chat App"})
	if out["was_current"] != true {
		t.Fatalf("revoke current: %v", out)
	}
	if _, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_sites", Arguments: map[string]any{}}); err == nil {
		t.Fatal("the revoked grant must stop working on the next call")
	}
	if _, _, err := r.env.Svc.AuthenticateMCP(ctx, access, "1.1.1.1"); err == nil {
		t.Fatal("revoked access token accepted")
	}
}
