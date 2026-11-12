package service

import (
	"context"
	"testing"
)

// An administrator's assistant acts only on the administrator's own sites:
// both an OAuth access token and an API token presented to /mcp drop the
// administrator exception.
func TestMCPActorHasNoAdminPowers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	foreign, err := e.svc.CreateSite(ctx, u, "Foreign", "")
	if err != nil {
		t.Fatal(err)
	}
	own, err := e.svc.CreateSite(ctx, e.admin, "Own", "")
	if err != nil {
		t.Fatal(err)
	}
	// Direct administrator access still works.
	if _, err := e.svc.GetSite(ctx, e.admin, foreign.Site.ID); err != nil {
		t.Fatalf("admin direct: %v", err)
	}

	tok, err := e.svc.ExchangeOAuthCode(ctx, e.authorize(t, e.admin, "c"), verifier, "c", "https://chat.example/cb", "")
	if err != nil {
		t.Fatal(err)
	}
	api, err := e.svc.CreateToken(ctx, e.admin, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, bearer := range map[string]string{"oauth": tok.AccessToken, "api token": api.Value} {
		a, _, err := e.svc.AuthenticateMCP(ctx, bearer, "1.1.1.1")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !a.ViaMCP || a.IsAdmin() {
			t.Fatalf("%s: actor %+v must be MCP-scoped", name, a)
		}
		if _, err := e.svc.GetSite(ctx, a, own.Site.ID); err != nil {
			t.Fatalf("%s: own site: %v", name, err)
		}
		_, err = e.svc.GetSite(ctx, a, foreign.Site.ID)
		wantNotFound(t, err)
		wantNotFound(t, e.svc.DeleteSite(ctx, a, foreign.Site.ID))
		_, err = e.svc.Rollback(ctx, a, foreign.Site.ID, "")
		wantNotFound(t, err)
		_, _, err = e.svc.ListUsers(ctx, a, UserFilter{})
		wantNotFound(t, err)
		_, _, err = e.svc.ListAllSites(ctx, a, "", Page{})
		wantNotFound(t, err)
	}
	if _, err := e.svc.GetSite(ctx, u, foreign.Site.ID); err != nil {
		t.Fatalf("foreign site was touched: %v", err)
	}
}
