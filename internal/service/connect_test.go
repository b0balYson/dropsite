package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestConnectCodeFormat(t *testing.T) {
	code := domain.NewConnectCode()
	if len(code) != 15 || code[4] != '-' || code[9] != '-' {
		t.Fatalf("format: %q", code)
	}
	norm := domain.NormalizeConnectCode(" " + strings.ToLower(strings.ReplaceAll(code, "-", "")) + " ")
	if norm != strings.ReplaceAll(code, "-", "") {
		t.Fatalf("normalize: %q", norm)
	}
	for _, bad := range []string{"", "short", "IIII-LLLL-OOOOO", strings.Repeat("A", 65)} {
		if domain.NormalizeConnectCode(bad) != "" {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestExchangeConnectCode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	issued, err := e.svc.CreateConnectCode(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if !issued.ExpiresAt.Equal(e.clock.Now().Add(ConnectCodeTTL)) {
		t.Fatalf("expiry: %v", issued.ExpiresAt)
	}
	res, err := e.svc.ExchangeConnectCode(ctx, strings.ToLower(issued.Code), "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.User.ID != u.User.ID || !strings.HasPrefix(res.Token.Token.Name, "Connected ") {
		t.Fatalf("result: %+v", res)
	}
	if _, err := e.svc.AuthenticateToken(ctx, res.Token.Value, "1.1.1.1"); err != nil {
		t.Fatalf("token does not work: %v", err)
	}
	_, err = e.svc.ExchangeConnectCode(ctx, issued.Code, "x", "1.1.1.1")
	wantCode(t, err, domain.CodeConnectInvalid)

	rows, _, err := e.svc.ListAudit(ctx, e.admin, AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, r := range rows {
		actions[r.Action] = true
		if strings.Contains(string(mustJSON(t, r.Meta)), strings.ReplaceAll(issued.Code, "-", "")) {
			t.Fatal("audit contains the code")
		}
	}
	for _, a := range []string{"connect.code_create", "connect.code_use", "connect.code_failed", "token.create"} {
		if !actions[a] {
			t.Errorf("no audit %s", a)
		}
	}
}

func TestConnectCodeExpiresAndBlockedUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	c1, _ := e.svc.CreateConnectCode(ctx, u)
	c2, _ := e.svc.CreateConnectCode(ctx, u)
	e.clock.Advance(ConnectCodeTTL)
	_, err := e.svc.ExchangeConnectCode(ctx, c1.Code, "", "1.1.1.1")
	wantCode(t, err, domain.CodeConnectInvalid)
	c3, _ := e.svc.CreateConnectCode(ctx, u)
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ExchangeConnectCode(ctx, c3.Code, "", "1.1.1.1")
	wantCode(t, err, domain.CodeConnectInvalid)
	_ = c2
}

func TestConnectCodeRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	good, _ := e.svc.CreateConnectCode(ctx, u)
	for range FailedCodesPerIP {
		_, err := e.svc.ExchangeConnectCode(ctx, "AAAA-AAAA-AAAAA", "", "9.9.9.9")
		wantCode(t, err, domain.CodeConnectInvalid)
	}
	_, err := e.svc.ExchangeConnectCode(ctx, good.Code, "", "9.9.9.9")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("want rate limited, got %v", err)
	}
	// The code was not consumed: another IP can still use it.
	if _, err := e.svc.ExchangeConnectCode(ctx, good.Code, "", "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(FailedCodesWindow + time.Second)
	_, err = e.svc.ExchangeConnectCode(ctx, "AAAA-AAAA-AAAAA", "", "9.9.9.9")
	wantCode(t, err, domain.CodeConnectInvalid)
}
