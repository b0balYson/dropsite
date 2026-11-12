package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// mcpActor authenticates a's API token on the MCP endpoint.
func (e *env) mcpActor(a Actor) Actor {
	e.t.Helper()
	tok, err := e.svc.CreateToken(context.Background(), a, "assistant", 0)
	if err != nil {
		e.t.Fatal(err)
	}
	m, _, err := e.svc.AuthenticateMCP(context.Background(), tok.Value, "1.1.1.1")
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func wantInvalid(t *testing.T, err error, substr string) {
	t.Helper()
	de := domain.AsError(err)
	if err == nil || de.Kind != domain.KindInvalid || !strings.Contains(de.Message, substr) {
		t.Fatalf("want invalid error containing %q, got %v", substr, err)
	}
}

// An administrator's assistant may move the base domain root, and only that:
// it finds any user's site for this operation, must repeat the site in
// confirm, and the other sites keep their addresses.
func TestRootSiteViaMCP(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	s1, _ := e.svc.CreateSite(ctx, u, "one", "one")
	s2, _ := e.svc.CreateSite(ctx, u, "two", "")
	_, _ = e.svc.Deploy(ctx, u, s1.Site.ID, zipOf(t, map[string]string{"index.html": "one"}), domain.SourceUI)
	_, _ = e.svc.Deploy(ctx, u, s2.Site.ID, zipOf(t, map[string]string{"index.html": "two"}), domain.SourceUI)

	// A regular user's assistant gets a clear refusal, checked before any
	// lookup, so a missing site gives the same answer as an existing one.
	userMCP := e.mcpActor(u)
	for _, ref := range []string{s1.Site.ID, "missing"} {
		_, err := e.svc.SetRootSite(ctx, userMCP, ref, ref)
		wantInvalid(t, err, "only an administrator")
	}
	_, err := e.svc.ClearRootSite(ctx, userMCP, s1.Site.ID)
	wantInvalid(t, err, "only an administrator")
	if _, _, err := e.svc.RootSite(ctx, userMCP); err == nil {
		t.Fatal("root site is admin only")
	}

	adminMCP := e.mcpActor(e.admin)
	if adminMCP.IsAdmin() {
		t.Fatal("MCP actor must not be an administrator")
	}
	// Confirm must name the site.
	_, err = e.svc.SetRootSite(ctx, adminMCP, s1.Site.ID, "two")
	wantInvalid(t, err, "confirm")
	if _, err := e.served(testBase, "index.html"); !os.IsNotExist(err) {
		t.Fatal("unconfirmed root must not change")
	}
	_, err = e.svc.SetRootSite(ctx, adminMCP, "missing", "missing")
	wantNotFound(t, err)

	v, err := e.svc.SetRootSite(ctx, adminMCP, "one", "ONE")
	if err != nil || v.Site.ID != s1.Site.ID {
		t.Fatalf("set root: %v %+v", err, v.Site)
	}
	if body, _ := e.served(testBase, "index.html"); body != "one" {
		t.Fatal("root must serve site one")
	}
	if _, err := e.svc.SetRootSite(ctx, adminMCP, s2.Site.ID, s2.Site.ID); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		testBase: "two", "one." + testBase: "one", s1.Site.ID + "." + testBase: "one", s2.Site.ID + "." + testBase: "two",
	} {
		if body, _ := e.served(host, "index.html"); body != want {
			t.Fatalf("%s serves %q, want %q", host, body, want)
		}
	}
	root, ok, err := e.svc.RootSite(ctx, adminMCP)
	if err != nil || !ok || root.Site.ID != s2.Site.ID {
		t.Fatalf("root site: %v %v %+v", err, ok, root.Site)
	}

	// Other administrator powers stay closed to the assistant.
	_, err = e.svc.SetApex(ctx, adminMCP, s1.Site.ID)
	wantNotFound(t, err)
	_, err = e.svc.ClearApex(ctx, adminMCP, s2.Site.ID)
	wantNotFound(t, err)
	_, err = e.svc.TransferSite(ctx, adminMCP, s1.Site.ID, e.admin.User.ID)
	wantNotFound(t, err)
	_, err = e.svc.GetSite(ctx, adminMCP, s1.Site.ID)
	wantNotFound(t, err)
	_, _, err = e.svc.ListAllSites(ctx, adminMCP, "", Page{})
	wantNotFound(t, err)

	_, err = e.svc.ClearRootSite(ctx, adminMCP, "one")
	wantInvalid(t, err, "confirm")
	if _, err := e.svc.ClearRootSite(ctx, adminMCP, s2.Site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.served(testBase, "index.html"); !os.IsNotExist(err) {
		t.Fatal("cleared root must not serve")
	}
	if body, _ := e.served(s2.Site.ID+"."+testBase, "index.html"); body != "two" {
		t.Fatal("site two keeps its subdomain")
	}
	if _, ok, err := e.svc.RootSite(ctx, adminMCP); err != nil || ok {
		t.Fatalf("no root expected: %v %v", ok, err)
	}
	_, err = e.svc.ClearRootSite(ctx, adminMCP, s2.Site.ID)
	wantInvalid(t, err, "not assigned")

	for _, action := range []string{"site.apex.set", "site.apex.clear"} {
		metas := e.auditMeta(action)
		if len(metas) == 0 {
			t.Fatalf("no %s audit entries", action)
		}
		for _, m := range metas {
			if m["source"] != "mcp" {
				t.Fatalf("%s meta %v must name source mcp", action, m)
			}
		}
	}
}

// Changes made through MCP are marked in the audit log; direct ones are not,
// and the caller's meta map is never modified.
func TestAuditSourceMCP(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	m := e.mcpActor(u)
	if m.TokenID == 0 || m.GrantID != 0 {
		t.Fatalf("API token actor: %+v", m)
	}
	s, err := e.svc.CreateSite(ctx, m, "via mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSite(ctx, u, "direct", ""); err != nil {
		t.Fatal(err)
	}
	var sources []any
	for _, meta := range e.auditMeta("site.create") {
		sources = append(sources, meta["source"])
	}
	if len(sources) != 2 || (sources[0] == "mcp") == (sources[1] == "mcp") {
		t.Fatalf("sources %v", sources)
	}
	meta := map[string]any{"k": 1}
	e.svc.audit(ctx, m, "test.action", siteTarget(s.Site.ID), meta)
	if _, ok := meta["source"]; ok {
		t.Fatal("audit mutated the caller's meta")
	}
	e.svc.audit(ctx, m, "test.nil", "", nil)
	if got := e.auditMeta("test.nil"); len(got) != 1 || got[0]["source"] != "mcp" {
		t.Fatalf("nil meta: %v", got)
	}
	e.svc.audit(ctx, m, "test.keep", "", map[string]any{"source": "api"})
	if got := e.auditMeta("test.keep"); got[0]["source"] != "api" {
		t.Fatalf("existing source overwritten: %v", got)
	}

	tok, err := e.svc.ExchangeOAuthCode(ctx, e.authorize(t, u, "c"), verifier, "c", "https://chat.example/cb", "")
	if err != nil {
		t.Fatal(err)
	}
	oa, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1")
	if err != nil || oa.GrantID == 0 || oa.TokenID != 0 {
		t.Fatalf("oauth actor: %+v %v", oa, err)
	}
	if !errors.Is(e.svc.RevokeOAuthGrant(ctx, oa, oa.GrantID), nil) {
		t.Fatal("revoke own grant")
	}
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("revoked grant must stop working at once")
	}
}
