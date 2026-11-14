package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func linkRequest(resource string) AuthorizeRequest {
	return AuthorizeRequest{
		ClientID: "dsc_x", ClientName: "Chat", RedirectURI: "https://chat.example/cb",
		Challenge: PKCEChallenge(verifier), Resource: resource,
	}
}

func TestConnectLinkParsing(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{
		"https://api.pp.test/mcp/c/ABCDEFGHJKMNP":    "ABCDEFGHJKMNP",
		"https://api.pp.test/mcp/c/abcd-efgh-jkmnp":  "ABCDEFGHJKMNP",
		"https://api.pp.test/mcp/c/ABCDEFGHJKMNP/":   "ABCDEFGHJKMNP",
		"https://api.pp.test/mcp":                    "",
		"https://api.pp.test/mcp/c/":                 "",
		"https://api.pp.test/mcp/c/SHORT":            "",
		"https://api.pp.test/mcp/c/ABCDEFGHJKMNP/x":  "",
		"https://api.pp.test/mcp/c/ABCD%20EFGHJKMNP": "",
		"https://evil.example/mcp/c/ABCDEFGHJKMNP":   "",
		"https://api.pp.test/mcp/x/ABCDEFGHJKMNP":    "",
		"https://api.pp.test/mcp/c/IIIIIIIIIIIII":    "",
	}
	for in, want := range cases {
		if got := e.svc.LinkCode(in); got != want {
			t.Errorf("LinkCode(%q) = %q, want %q", in, got, want)
		}
		res, ok := e.svc.CanonicalResource(in)
		switch {
		case want != "" && (!ok || res != "https://api.pp.test/mcp"):
			t.Errorf("CanonicalResource(%q) = %q %v", in, res, ok)
		case want == "" && strings.Contains(in, "/c/") && ok:
			t.Errorf("CanonicalResource(%q) accepted", in)
		}
	}
	if got := e.svc.ConnectURL("abcd-efgh-jkmnp"); got != "https://api.pp.test/mcp/c/ABCDEFGHJKMNP" {
		t.Fatalf("ConnectURL: %s", got)
	}
}

func TestConnectURLConsumeOnceAndAlias(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	link, err := e.svc.CreateConnectURL(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if !link.ExpiresAt.Equal(e.clock.Now().Add(ConnectURLTTL)) {
		t.Fatalf("expiry: %v", link.ExpiresAt)
	}
	code := e.svc.LinkCode(link.URL)
	if code == "" {
		t.Fatalf("not a connect URL: %s", link.URL)
	}
	// Checking does not consume, however often.
	for range 3 {
		if err := e.svc.CheckConnectLink(ctx, code, "1.1.1.1"); err != nil {
			t.Fatal(err)
		}
	}
	req := linkRequest(link.URL)
	if _, err := e.svc.AuthorizeOAuthLink(ctx, req, code, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	// Used: neither the check nor a second grant passes.
	wantCode(t, e.svc.CheckConnectLink(ctx, code, "1.1.1.1"), domain.CodeConnectInvalid)
	_, err = e.svc.AuthorizeOAuthLink(ctx, req, code, "1.1.1.1")
	wantCode(t, err, domain.CodeConnectInvalid)
	// The URL stays an alias: a code typed for that resource still works.
	if _, err := e.svc.AuthorizeOAuth(ctx, req, mustCode(t, e, u), "1.1.1.1"); err != nil {
		t.Fatalf("alias resource with a typed code: %v", err)
	}

	rows, _, err := e.svc.ListAudit(ctx, e.admin, AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Action] = true
		if strings.Contains(string(mustJSON(t, r.Meta)), code) || strings.Contains(r.Target, code) {
			t.Fatal("audit contains the code")
		}
		if r.Action == "connect.code_use" && r.Meta["purpose"] == "oauth_link" {
			seen["use_link"] = true
		}
	}
	for _, a := range []string{"connect.url_create", "use_link", "oauth.authorize", "connect.code_failed"} {
		if !seen[a] {
			t.Errorf("no audit %s", a)
		}
	}
}

func mustCode(t *testing.T, e *env, a Actor) string {
	t.Helper()
	c, err := e.svc.CreateConnectCode(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return c.Code
}

func TestConnectURLExpiresAfterAnHour(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	link, _ := e.svc.CreateConnectURL(ctx, u)
	code := e.svc.LinkCode(link.URL)
	// Longer than a typed code...
	e.clock.Advance(ConnectCodeTTL + time.Minute)
	if err := e.svc.CheckConnectLink(ctx, code, "1.1.1.1"); err != nil {
		t.Fatalf("expired after %v: %v", ConnectCodeTTL, err)
	}
	// ...but not past an hour.
	e.clock.Advance(ConnectURLTTL)
	wantCode(t, e.svc.CheckConnectLink(ctx, code, "1.1.1.1"), domain.CodeConnectInvalid)
	_, err := e.svc.AuthorizeOAuthLink(ctx, linkRequest(link.URL), code, "1.1.1.1")
	wantCode(t, err, domain.CodeConnectInvalid)
}

func TestConnectLinkLockoutIsShared(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	link, _ := e.svc.CreateConnectURL(ctx, u)
	for range FailedCodesPerIP {
		wantCode(t, e.svc.CheckConnectLink(ctx, "ABCDEFGHJKMNP", "9.9.9.9"), domain.CodeConnectInvalid)
	}
	// Locked out for links and typed codes alike, valid or not.
	if err := e.svc.CheckConnectLink(ctx, e.svc.LinkCode(link.URL), "9.9.9.9"); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("link during lockout: %v", err)
	}
	if _, err := e.svc.ExchangeConnectCode(ctx, mustCode(t, e, u), "", "9.9.9.9"); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("typed code during lockout: %v", err)
	}
	// Another network is not affected.
	if err := e.svc.CheckConnectLink(ctx, e.svc.LinkCode(link.URL), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	// A blocked owner's link fails like an unknown one.
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.svc.CheckConnectLink(ctx, e.svc.LinkCode(link.URL), "7.7.7.7"), domain.CodeConnectInvalid)
}

func TestAuthorizeOAuthSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	req := linkRequest("https://api.pp.test/mcp")
	for _, a := range []Actor{{}, {User: u.User, ViaMCP: true}, {User: u.User, TokenID: 3}, {User: u.User, GrantID: 4}} {
		if _, err := e.svc.AuthorizeOAuthSession(ctx, a, req); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("actor %+v: %v", a, err)
		}
	}
	bad := req
	bad.Resource = "https://evil.example/mcp"
	_, err := e.svc.AuthorizeOAuthSession(ctx, u, bad)
	wantOAuth(t, err, "invalid_target")
	code, err := e.svc.AuthorizeOAuthSession(ctx, u, req)
	if err != nil || code == "" {
		t.Fatalf("session approve: %q %v", code, err)
	}
	if _, err := e.svc.ExchangeOAuthCode(ctx, code, verifier, "dsc_x", "https://chat.example/cb", ""); err != nil {
		t.Fatalf("exchange: %v", err)
	}
}

func TestCreateConnectURLForEmail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	link, got, err := e.svc.CreateConnectURLForEmail(ctx, " U@pp.test ")
	if err != nil || got.ID != u.User.ID || e.svc.LinkCode(link.URL) == "" {
		t.Fatalf("for email: %+v %v %v", link, got, err)
	}
	if _, _, err := e.svc.CreateConnectURLForEmail(ctx, "nobody@pp.test"); domain.AsError(err).Kind != domain.KindNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.CreateConnectURLForEmail(ctx, "u@pp.test")
	wantCode(t, err, domain.CodeConnectInvalid)
	if _, err := e.svc.CreateConnectURL(ctx, Actor{User: u.User, ViaMCP: true}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("via MCP: %v", err)
	}
}

func TestRedactConnectLinks(t *testing.T) {
	for in, want := range map[string]string{
		"/mcp/c/ABCDEFGHJKMNP": "/mcp/c/REDACTED",
		"/.well-known/oauth-protected-resource/mcp/c/abcd-efgh":                       "/.well-known/oauth-protected-resource/mcp/c/REDACTED",
		"/oauth/authorize?resource=https%3A%2F%2Fapi.x%2Fmcp%2Fc%2FABCDEFGHJKMNP&x=1": "/oauth/authorize?resource=https%3A%2F%2Fapi.x%2Fmcp%2Fc%2FREDACTED&x=1",
		"/mcp": "/mcp",
	} {
		if got := domain.RedactConnectLinks(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
