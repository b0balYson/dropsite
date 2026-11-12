package store

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestRevokeUserTokens(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@example.test", domain.UserActive)
	other := mkUser(t, db, "b@example.test", domain.UserActive)
	mk := func(user int64, hash string) int64 {
		id, err := db.CreateToken(ctx, domain.Token{UserID: user, Name: hash, Prefix: "ds_" + hash, Hash: []byte(hash), CreatedAt: t0})
		must(t, err)
		return id
	}
	mk(u, "u1")
	old := mk(u, "u2")
	mk(other, "o1")
	must(t, db.RevokeToken(ctx, u, old, t0))

	n, err := db.RevokeUserTokens(ctx, u, t0.Add(time.Hour))
	must(t, err)
	if n != 1 {
		t.Fatalf("revoked %d, want 1 (already revoked tokens are not counted)", n)
	}
	list, err := db.ListTokens(ctx, u)
	must(t, err)
	for _, tok := range list {
		if tok.RevokedAt == nil {
			t.Fatalf("token %d still active", tok.ID)
		}
		if tok.ID == old && !tok.RevokedAt.Equal(t0) {
			t.Fatalf("earlier revocation time overwritten: %v", tok.RevokedAt)
		}
	}
	others, err := db.ListTokens(ctx, other)
	must(t, err)
	if len(others) != 1 || others[0].RevokedAt != nil {
		t.Fatal("other users' tokens must stay active")
	}
	if n, err := db.RevokeUserTokens(ctx, u, t0.Add(2*time.Hour)); err != nil || n != 0 {
		t.Fatalf("second call: %d %v", n, err)
	}
}
