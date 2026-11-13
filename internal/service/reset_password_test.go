package service

import (
	"context"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestResetPassword(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.ResetPassword(ctx, "admin@pp.test", "short"); err == nil {
		t.Fatal("short password must fail")
	} else {
		wantCode(t, err, domain.CodePasswordShort)
	}
	if _, err := e.svc.ResetPassword(ctx, "nobody@pp.test", "long-enough-pw"); err == nil {
		t.Fatal("unknown user must fail")
	}
	u, err := e.svc.ResetPassword(ctx, " ADMIN@pp.test ", "brand-new-password")
	if err != nil || u.Status != domain.UserActive {
		t.Fatalf("reset: %+v %v", u, err)
	}
	if _, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "1.1.1.2", ""); err == nil {
		t.Fatal("old password must stop working")
	}
	if _, err := e.svc.Login(ctx, "admin@pp.test", "brand-new-password", "1.1.1.3", ""); err != nil {
		t.Fatalf("new password: %v", err)
	}

	inv, err := e.svc.CreateUser(ctx, e.admin, "carol@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	u, err = e.svc.ResetPassword(ctx, inv.User.Email, "carols-password")
	if err != nil || u.Status != domain.UserActive {
		t.Fatalf("invited user must become active: %+v %v", u, err)
	}
	if _, err := e.svc.Login(ctx, "carol@example.com", "carols-password", "1.1.1.4", ""); err != nil {
		t.Fatalf("invited user login: %v", err)
	}
}

func TestResetPasswordEndsSessions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "1.1.1.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResetPassword(ctx, "admin@pp.test", "brand-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateSession(ctx, res.Secret, "1.1.1.1"); err == nil {
		t.Fatal("session must end after a password reset")
	}
}
