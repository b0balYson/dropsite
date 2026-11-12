package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestConnectCodeRedeemOnce(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	uid := mkUser(t, db, "a@example.com", domain.UserActive)
	hash := domain.HashSecret("CODE")
	if _, err := db.CreateConnectCode(ctx, domain.ConnectCode{UserID: uid, Hash: hash, CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RedeemConnectCode(ctx, hash, t0.Add(10*time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired code: got %v", err)
	}
	got, err := db.RedeemConnectCode(ctx, hash, t0.Add(time.Minute))
	if err != nil || got != uid {
		t.Fatalf("redeem: %d %v", got, err)
	}
	if _, err := db.RedeemConnectCode(ctx, hash, t0.Add(time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second redeem: got %v", err)
	}
	n, err := db.DeleteExpiredConnectCodes(ctx, t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("delete expired: %d %v", n, err)
	}
}

func TestOAuthClients(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	c := domain.OAuthClient{ID: "dsc_x", Name: "App", RedirectURIs: []string{"https://a.example/cb"}, CreatedAt: t0, CreatedIP: "1.2.3.4"}
	if err := db.CreateOAuthClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetOAuthClient(ctx, "dsc_x")
	if err != nil || got.Name != "App" || len(got.RedirectURIs) != 1 || !got.CreatedAt.Equal(t0) {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := db.GetOAuthClient(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if n, err := db.CountOAuthClients(ctx); err != nil || n != 1 {
		t.Fatalf("count: %d %v", n, err)
	}
	if n, err := db.DeleteUnusedOAuthClients(ctx, t0.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("delete unused: %d %v", n, err)
	}
}

func issue(t *testing.T, db *DB, uid int64, codeHash string) (domain.OAuthCode, int64) {
	t.Helper()
	ctx := context.Background()
	_, err := db.CreateOAuthCode(ctx, domain.OAuthCode{
		Hash: domain.HashSecret(codeHash), UserID: uid, ClientID: "c1", ClientName: "App",
		RedirectURI: "https://a.example/cb", Challenge: "ch", Resource: "https://api/mcp", Scope: "sites",
		CreatedAt: t0, ExpiresAt: t0.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.RedeemOAuthCode(ctx, domain.HashSecret(codeHash), t0)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := db.IssueOAuthGrant(ctx, c.ID, domain.OAuthGrant{
		UserID: uid, ClientID: "c1", ClientName: "App", RedirectHost: "a.example", Scope: "sites",
		Resource: "https://api/mcp", CreatedAt: t0,
	}, []domain.OAuthToken{
		{Hash: domain.HashSecret(codeHash + "A"), Kind: domain.OAuthAccess, CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)},
		{Hash: domain.HashSecret(codeHash + "R"), Kind: domain.OAuthRefresh, CreatedAt: t0, ExpiresAt: t0.Add(24 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, gid
}

func TestOAuthCodeReuseRevokesGrant(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	uid := mkUser(t, db, "a@example.com", domain.UserActive)
	_, gid := issue(t, db, uid, "code1")
	if _, err := db.RedeemOAuthCode(ctx, domain.HashSecret("code1"), t0); !errors.Is(err, ErrCredentialReused) {
		t.Fatalf("reuse: %v", err)
	}
	_, g, err := db.GetOAuthToken(ctx, domain.HashSecret("code1A"))
	if err != nil || g.ID != gid || g.RevokedAt == nil {
		t.Fatalf("grant after reuse: %+v %v", g, err)
	}
	if _, err := db.RedeemOAuthCode(ctx, domain.HashSecret("unknown"), t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestOAuthCodeExpired(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	uid := mkUser(t, db, "a@example.com", domain.UserActive)
	if _, err := db.CreateOAuthCode(ctx, domain.OAuthCode{Hash: []byte("h"), UserID: uid, CreatedAt: t0, ExpiresAt: t0.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RedeemOAuthCode(ctx, []byte("h"), t0.Add(time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
}

func TestOAuthRefreshRotationAndReuse(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	uid := mkUser(t, db, "a@example.com", domain.UserActive)
	_, gid := issue(t, db, uid, "c")
	r1, _, err := db.GetOAuthToken(ctx, domain.HashSecret("cR"))
	if err != nil {
		t.Fatal(err)
	}
	next := []domain.OAuthToken{{Hash: []byte("r2"), Kind: domain.OAuthRefresh, CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}}
	if err := db.RotateOAuthRefresh(ctx, r1.ID, gid, t0.Add(time.Minute), next); err != nil {
		t.Fatal(err)
	}
	if err := db.RotateOAuthRefresh(ctx, r1.ID, gid, t0.Add(2*time.Minute), nil); !errors.Is(err, ErrCredentialReused) {
		t.Fatalf("reuse: %v", err)
	}
	_, g, err := db.GetOAuthToken(ctx, []byte("r2"))
	if err != nil || g.RevokedAt == nil {
		t.Fatalf("chain not revoked: %+v %v", g, err)
	}
	if n, err := db.DeleteExpiredOAuth(ctx, t0.Add(3*time.Minute)); err != nil || n == 0 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
}

func TestOAuthGrantsListAndRevoke(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	uid := mkUser(t, db, "a@example.com", domain.UserActive)
	other := mkUser(t, db, "b@example.com", domain.UserActive)
	_, first := issue(t, db, uid, "x1")
	_, gid := issue(t, db, uid, "x2") // same client: replaces the first grant
	list, err := db.ListOAuthGrants(ctx, uid)
	if err != nil || len(list) != 1 || list[0].ID != gid || list[0].ID == first {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := db.TouchOAuthGrant(ctx, gid, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RevokeUserOAuthGrant(ctx, other, gid, t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign revoke: %v", err)
	}
	g, err := db.RevokeUserOAuthGrant(ctx, uid, gid, t0)
	if err != nil || g.ClientName != "App" || g.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", g, err)
	}
	if _, err := db.RevokeUserOAuthGrant(ctx, uid, gid, t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
	if err := db.RevokeOAuthGrant(ctx, gid, t0); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListOAuthGrants(ctx, uid); len(list) != 0 {
		t.Fatalf("list after revoke: %+v", list)
	}
}
