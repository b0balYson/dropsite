package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		p, err := generatePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != passwordLength {
			t.Fatalf("length %d", len(p))
		}
		for _, c := range p {
			if !strings.ContainsRune(passwordAlphabet, c) {
				t.Fatalf("character %q outside the alphabet", c)
			}
		}
		if seen[p] {
			t.Fatal("repeated password")
		}
		seen[p] = true
	}
}

func TestResetPasswordCmd(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	a, err := build(ctx, cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	_ = a.db.Close()
	t.Setenv("BASE_DOMAIN", "pp.test")
	t.Setenv("ADMIN_EMAIL", "admin@pp.test")
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("SITES_DIR", cfg.SitesDir)
	t.Setenv("STATE_DIR", cfg.StateDir)

	if err := resetPasswordCmd(ctx, []string{"a", "b"}, &bytes.Buffer{}); !errors.Is(err, errUsage) {
		t.Fatalf("two arguments: want usage, got %v", err)
	}
	if err := resetPasswordCmd(ctx, []string{"nobody@pp.test"}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "no user") {
		t.Fatalf("unknown user: %v", err)
	}

	var out bytes.Buffer
	if err := resetPasswordCmd(ctx, nil, &out); err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(out.String())
	if len(password) != passwordLength || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("stdout must hold only the password: %q", out.String())
	}

	a, err = build(ctx, cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	if _, err := a.svc.Login(ctx, "admin@pp.test", "admin-password", "127.0.0.1", ""); err == nil {
		t.Fatal("old password must stop working")
	}
	if _, err := a.svc.Login(ctx, "admin@pp.test", password, "127.0.0.1", ""); err != nil {
		t.Fatalf("printed password must work: %v", err)
	}
}
