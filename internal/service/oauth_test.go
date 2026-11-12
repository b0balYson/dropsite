package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func wantOAuth(t *testing.T, err error, code string) {
	t.Helper()
	var oe *OAuthError
	if !errors.As(err, &oe) || oe.Code != code {
		t.Fatalf("want oauth error %s, got %v", code, err)
	}
}

const verifier = "verifier-0123456789-0123456789-0123456789-abcdef"

func (e *env) authorize(t *testing.T, a Actor, client string) string {
	t.Helper()
	ctx := context.Background()
	c, err := e.svc.CreateConnectCode(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	code, err := e.svc.AuthorizeOAuth(ctx, AuthorizeRequest{
		ClientID: client, ClientName: "Chat  App", RedirectURI: "https://chat.example/cb",
		Challenge: PKCEChallenge(verifier), Resource: "https://api.pp.test/mcp",
	}, c.Code, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestRegisterOAuthClient(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, bad := range [][]string{nil, {"http://example.com/cb"}, {"https://a/cb#x"}, {"javascript:alert(1)"}, {"https://u:p@a/cb"}} {
		_, err := e.svc.RegisterOAuthClient(ctx, "x", bad, "1.1.1.1")
		wantOAuth(t, err, "invalid_redirect_uri")
	}
	c, err := e.svc.RegisterOAuthClient(ctx, "", []string{"http://127.0.0.1:3000/cb", "https://a.example/cb"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "127.0.0.1:3000" {
		t.Fatalf("default name: %q", c.Name)
	}
	got, err := e.svc.GetOAuthClient(ctx, c.ID)
	if err != nil || len(got.RedirectURIs) != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	for range RegistrationsPerIP - 1 {
		if _, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a/cb"}, "1.1.1.1"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a/cb"}, "1.1.1.1")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("want rate limit, got %v", err)
	}
}

func TestCanonicalResource(t *testing.T) {
	e := newEnv(t)
	for in, ok := range map[string]bool{
		"": true, "https://api.pp.test": true, "https://api.pp.test/": true, "https://api.pp.test/mcp": true,
		"https://api.pp.test/mcp/": true, "https://evil.test/mcp": false, "https://api.pp.test/other": false,
	} {
		got, gotOK := e.svc.CanonicalResource(in)
		if gotOK != ok || (ok && got != "https://api.pp.test/mcp") {
			t.Errorf("%q: %q %v", in, got, gotOK)
		}
	}
}

func TestOAuthFlowRotationAndReuse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	code := e.authorize(t, u, "client-1")

	_, err := e.svc.ExchangeOAuthCode(ctx, code, verifier, "other", "https://chat.example/cb", "")
	wantOAuth(t, err, "invalid_grant") // the code is consumed by this attempt
	code = e.authorize(t, u, "client-1")
	_, err = e.svc.ExchangeOAuthCode(ctx, code, strings.Repeat("x", 43), "client-1", "https://chat.example/cb", "")
	wantOAuth(t, err, "invalid_grant")

	code = e.authorize(t, u, "client-1")
	tok, err := e.svc.ExchangeOAuthCode(ctx, code, verifier, "client-1", "https://chat.example/cb", "https://api.pp.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresIn != 3600 || tok.Scope != OAuthScope {
		t.Fatalf("tokens: %+v", tok)
	}
	a, exp, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1")
	if err != nil || a.User.ID != u.User.ID || !exp.Equal(e.clock.Now().Add(AccessTokenTTL)) {
		t.Fatalf("authenticate: %+v %v %v", a, exp, err)
	}
	// OAuth tokens are not API tokens.
	if _, err := e.svc.AuthenticateToken(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("oauth token accepted by the API")
	}
	// Code reuse revokes the grant.
	_, err = e.svc.ExchangeOAuthCode(ctx, code, verifier, "client-1", "https://chat.example/cb", "")
	wantOAuth(t, err, "invalid_grant")
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("token survived code reuse")
	}

	code = e.authorize(t, u, "client-1")
	tok, err = e.svc.ExchangeOAuthCode(ctx, code, verifier, "client-1", "https://chat.example/cb", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.RefreshOAuth(ctx, tok.RefreshToken, "client-2", "")
	wantOAuth(t, err, "invalid_grant")
	_, err = e.svc.RefreshOAuth(ctx, tok.RefreshToken, "client-1", "https://evil.test")
	wantOAuth(t, err, "invalid_target")
	tok2, err := e.svc.RefreshOAuth(ctx, tok.RefreshToken, "client-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok2.AccessToken, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.RefreshOAuth(ctx, tok.RefreshToken, "client-1", "")
	wantOAuth(t, err, "invalid_grant")
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok2.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("chain not revoked after refresh reuse")
	}
	if _, err := e.svc.RefreshOAuth(ctx, tok2.RefreshToken, "client-1", ""); err == nil {
		t.Fatal("rotated refresh still works after reuse")
	}
}

func TestOAuthGrantsListRevokeAndExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	other := e.user("o@pp.test")
	tok, err := e.svc.ExchangeOAuthCode(ctx, e.authorize(t, u, "c"), verifier, "c", "https://chat.example/cb", "")
	if err != nil {
		t.Fatal(err)
	}
	grants, err := e.svc.ListOAuthGrants(ctx, u)
	if err != nil || len(grants) != 1 || grants[0].ClientName != "Chat App" || grants[0].RedirectHost != "chat.example" {
		t.Fatalf("grants: %+v %v", grants, err)
	}
	wantNotFound(t, e.svc.RevokeOAuthGrant(ctx, other, grants[0].ID))

	e.clock.Advance(AccessTokenTTL)
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("expired access token accepted")
	}
	tok, err = e.svc.RefreshOAuth(ctx, tok.RefreshToken, "c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeOAuthGrant(ctx, u, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("revoked grant still works")
	}
	if g, _ := e.svc.ListOAuthGrants(ctx, u); len(g) != 0 {
		t.Fatalf("list after revoke: %+v", g)
	}
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeOAuthTokenAndBlockedUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	tok, err := e.svc.ExchangeOAuthCode(ctx, e.authorize(t, u, "c"), verifier, "c", "https://chat.example/cb", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, junk := range []string{"", "ds_x", "dsa_unknown"} {
		if err := e.svc.RevokeOAuthToken(ctx, junk); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.RevokeOAuthToken(ctx, tok.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("revoked via endpoint still works")
	}
	tok, _ = e.svc.ExchangeOAuthCode(ctx, e.authorize(t, u, "c"), verifier, "c", "https://chat.example/cb", "")
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateMCP(ctx, tok.AccessToken, "1.1.1.1"); err == nil {
		t.Fatal("blocked user's token accepted")
	}
	_, err = e.svc.RefreshOAuth(ctx, tok.RefreshToken, "c", "")
	wantOAuth(t, err, "invalid_grant")
}

func TestAuthorizeOAuthValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.svc.AuthorizeOAuth(ctx, AuthorizeRequest{ClientID: "c", RedirectURI: "https://a/cb"}, "x", "1.1.1.1")
	wantOAuth(t, err, "invalid_request")
	_, err = e.svc.AuthorizeOAuth(ctx, AuthorizeRequest{ClientID: "c", RedirectURI: "https://a/cb", Challenge: "x", Resource: "https://x"}, "x", "1.1.1.1")
	wantOAuth(t, err, "invalid_target")
	_, err = e.svc.AuthorizeOAuth(ctx, AuthorizeRequest{ClientID: "c", RedirectURI: "https://a/cb", Challenge: "x"}, "AAAA-AAAA-AAAAA", "1.1.1.1")
	wantCode(t, err, domain.CodeConnectInvalid)
	// API tokens also authenticate MCP.
	u := e.user("u@pp.test")
	nt, err := e.svc.CreateToken(ctx, u, "t", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a, exp, err := e.svc.AuthenticateMCP(ctx, nt.Value, "1.1.1.1"); err != nil || a.User.ID != u.User.ID || !exp.IsZero() {
		t.Fatalf("api token: %v", err)
	}
	if _, _, err := e.svc.AuthenticateMCP(ctx, "garbage", "1.1.1.1"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("garbage: %v", err)
	}
	_ = time.Second
}
