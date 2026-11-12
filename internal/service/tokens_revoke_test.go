package service

import (
	"context"
	"errors"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestRevokeUserTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	bob := e.user("bob@pp.test")
	t1, err := e.svc.CreateToken(ctx, ann, "ci", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateToken(ctx, ann, "laptop", 0); err != nil {
		t.Fatal(err)
	}
	bobTok, err := e.svc.CreateToken(ctx, bob, "ci", 0)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.RevokeUserTokens(ctx, bob, ann.User.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-admins must get not found, got %v", err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, t1.Value, "10.0.0.9"); err != nil {
		t.Fatalf("a refused call must not revoke anything: %v", err)
	}
	wantNotFound(t, func() error { _, err := e.svc.RevokeUserTokens(ctx, e.admin, 999999); return err }())

	n, err := e.svc.RevokeUserTokens(ctx, e.admin, ann.User.ID)
	if err != nil || n != 2 {
		t.Fatalf("revoked %d, err %v; want 2", n, err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, t1.Value, "10.0.0.9"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked token still works: %v", err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, bobTok.Value, "10.0.0.9"); err != nil {
		t.Fatalf("other users' tokens are unaffected: %v", err)
	}
	metas := e.auditMeta("user.tokens_revoke")
	if len(metas) != 1 || metas[0]["count"] != float64(2) {
		t.Fatalf("audit = %v", metas)
	}
}
