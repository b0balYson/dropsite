package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestUsersCRUD(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	id := mkUser(t, db, "Alice@Example.io", domain.UserInvited)

	_, err := db.CreateUser(ctx, domain.User{Email: "alice@EXAMPLE.io", Role: domain.RoleUser,
		Status: domain.UserActive, CreatedAt: t0, UpdatedAt: t0})
	wantCode(t, err, domain.CodeEmailTaken)

	u, err := db.GetUserByEmail(ctx, "ALICE@example.IO")
	must(t, err)
	if u.ID != id || u.Email != "Alice@Example.io" || u.Status != domain.UserInvited || !u.CreatedAt.Equal(t0) ||
		u.CreatedAt.Location() != time.UTC {
		t.Fatalf("unexpected user %+v", u)
	}
	_, err = db.GetUserByEmail(ctx, "nobody@example.io")
	wantNotFound(t, err)
	_, err = db.GetUser(ctx, 999)
	wantNotFound(t, err)

	later := t0.Add(time.Hour)
	must(t, db.SetUserStatus(ctx, id, domain.UserBlocked, later))
	must(t, db.SetUserPassword(ctx, id, "hash", later))
	must(t, db.SetUserLang(ctx, id, "ru", later))
	u, err = db.GetUser(ctx, id)
	must(t, err)
	if u.Status != domain.UserBlocked || u.PasswordHash != "hash" || u.Lang != "ru" || !u.UpdatedAt.Equal(later) {
		t.Fatalf("updates not applied: %+v", u)
	}
	wantNotFound(t, db.SetUserStatus(ctx, 999, domain.UserActive, later))
	wantNotFound(t, db.SetUserPassword(ctx, 999, "x", later))
	wantNotFound(t, db.SetUserLang(ctx, 999, "en", later))

	n, err := db.CountUsers(ctx)
	must(t, err)
	if n != 1 {
		t.Fatalf("count = %d", n)
	}
}

func TestListUsers(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	a := mkUser(t, db, "a_b@x.io", domain.UserActive)
	mkUser(t, db, "axb@x.io", domain.UserInvited)
	mkUser(t, db, "c@y.io", domain.UserActive)
	mkSite(t, db, "site000001", a, "", t0)
	mkSite(t, db, "site000002", a, "", t0)
	mkSite(t, db, "site000003", a, "", t0)
	_, err := db.SoftDeleteSite(ctx, "site000003", t0)
	must(t, err)

	got, total, err := db.ListUsers(ctx, UserFilter{})
	must(t, err)
	if total != 3 || len(got) != 3 || got[0].ID != a || got[0].SiteCount != 2 || got[1].SiteCount != 0 {
		t.Fatalf("all users: total %d, %+v", total, got)
	}

	// "_" must be literal, not a wildcard.
	got, total, err = db.ListUsers(ctx, UserFilter{Query: "a_b"})
	must(t, err)
	if total != 1 || len(got) != 1 || got[0].Email != "a_b@x.io" {
		t.Fatalf("escaped query: total %d, %+v", total, got)
	}

	got, total, err = db.ListUsers(ctx, UserFilter{Query: "x.io", Status: domain.UserInvited})
	must(t, err)
	if total != 1 || got[0].Email != "axb@x.io" {
		t.Fatalf("status filter: total %d, %+v", total, got)
	}

	got, total, err = db.ListUsers(ctx, UserFilter{Page: Page{Limit: 1, Offset: 1}})
	must(t, err)
	if total != 3 || len(got) != 1 || got[0].Email != "axb@x.io" {
		t.Fatalf("paging: total %d, %+v", total, got)
	}
}

func TestPageBounds(t *testing.T) {
	cases := []struct {
		in            Page
		limit, offset int
	}{
		{Page{}, DefaultPageSize, 0},
		{Page{Limit: 10, Offset: 5}, 10, 5},
		{Page{Limit: MaxPageSize + 1}, MaxPageSize, 0},
		{Page{Limit: -3, Offset: -7}, DefaultPageSize, 0},
	}
	for _, c := range cases {
		l, o := c.in.bounds()
		if l != c.limit || o != c.offset {
			t.Errorf("%+v → (%d, %d), want (%d, %d)", c.in, l, o, c.limit, c.offset)
		}
	}
}

func newInvite(userID int64, hash string, created, expires time.Time) domain.Invite {
	return domain.Invite{UserID: userID, Hash: []byte(hash), CreatedBy: userID, CreatedAt: created, ExpiresAt: expires}
}

func TestCreateInviteRevokesPrevious(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserInvited)
	_, err := db.CreateInvite(ctx, newInvite(u, "h1", t0, t0.Add(time.Hour)))
	must(t, err)
	id2, err := db.CreateInvite(ctx, newInvite(u, "h2", t0.Add(time.Minute), t0.Add(time.Hour)))
	must(t, err)

	first, err := db.GetInviteByHash(ctx, []byte("h1"))
	must(t, err)
	if first.RevokedAt == nil || !first.RevokedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("first invite not revoked: %+v", first)
	}
	second, err := db.GetInviteByHash(ctx, []byte("h2"))
	must(t, err)
	if second.ID != id2 || second.RevokedAt != nil || !bytes.Equal(second.Hash, []byte("h2")) {
		t.Fatalf("second invite: %+v", second)
	}
	_, err = db.RedeemInvite(ctx, first.ID, "pw", t0.Add(2*time.Minute))
	wantCode(t, err, domain.CodeInviteInvalid)
	_, err = db.GetInviteByHash(ctx, []byte("missing"))
	wantNotFound(t, err)
	_, err = db.CreateInvite(ctx, newInvite(u, "h2", t0, t0.Add(time.Hour)))
	if err == nil {
		t.Fatal("duplicate invite hash must fail")
	}
}

func TestRedeemInvite(t *testing.T) {
	ctx := context.Background()
	exp := t0.Add(time.Hour)
	cases := []struct {
		name    string
		status  domain.UserStatus
		prepare func(t *testing.T, db *DB, inviteID int64)
		now     time.Time
		ok      bool
	}{
		{name: "valid invited", status: domain.UserInvited, now: t0, ok: true},
		{name: "valid active (reset)", status: domain.UserActive, now: t0, ok: true},
		{name: "expired", status: domain.UserInvited, now: exp},
		{name: "blocked user", status: domain.UserBlocked, now: t0},
		{name: "used", status: domain.UserInvited, now: t0, prepare: func(t *testing.T, db *DB, id int64) {
			_, err := db.RedeemInvite(ctx, id, "first", t0)
			must(t, err)
		}},
		{name: "revoked", status: domain.UserInvited, now: t0, prepare: func(t *testing.T, db *DB, id int64) {
			_, err := db.Writer().ExecContext(ctx, "UPDATE invites SET revoked_at = 1 WHERE id = ?", id)
			must(t, err)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newDB(t)
			u := mkUser(t, db, "u@x.io", c.status)
			id, err := db.CreateInvite(ctx, newInvite(u, "h", t0.Add(-time.Minute), exp))
			must(t, err)
			if c.prepare != nil {
				c.prepare(t, db, id)
			}
			got, err := db.RedeemInvite(ctx, id, "newhash", c.now)
			if !c.ok {
				wantCode(t, err, domain.CodeInviteInvalid)
				return
			}
			must(t, err)
			if got.ID != u || got.Status != domain.UserActive || got.PasswordHash != "newhash" || !got.UpdatedAt.Equal(c.now) {
				t.Fatalf("redeemed user %+v", got)
			}
			inv, err := db.GetInviteByHash(ctx, []byte("h"))
			must(t, err)
			if inv.UsedAt == nil || !inv.UsedAt.Equal(c.now) {
				t.Fatalf("invite not marked used: %+v", inv)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		_, err := newDB(t).RedeemInvite(ctx, 42, "x", t0)
		wantCode(t, err, domain.CodeInviteInvalid)
	})
}

func TestDeleteExpiredInvites(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserInvited)
	v := mkUser(t, db, "v@x.io", domain.UserInvited)
	_, err := db.CreateInvite(ctx, newInvite(u, "old", t0, t0.Add(time.Hour)))
	must(t, err)
	_, err = db.CreateInvite(ctx, newInvite(v, "new", t0, t0.Add(3*time.Hour)))
	must(t, err)
	n, err := db.DeleteExpiredInvites(ctx, t0.Add(2*time.Hour))
	must(t, err)
	if n != 1 {
		t.Fatalf("deleted %d", n)
	}
	_, err = db.GetInviteByHash(ctx, []byte("new"))
	must(t, err)
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	s := domain.Session{ID: []byte("s1"), UserID: u, CSRFSecret: []byte("c"), CreatedAt: t0,
		ExpiresAt: t0.Add(time.Hour), LastSeenAt: t0, IP: "1.2.3.4", UserAgent: "ua"}
	must(t, db.CreateSession(ctx, s))
	s2 := s
	s2.ID, s2.ExpiresAt = []byte("s2"), t0.Add(time.Minute)
	must(t, db.CreateSession(ctx, s2))

	must(t, db.TouchSession(ctx, s.ID, t0.Add(time.Minute), t0.Add(2*time.Hour)))
	got, err := db.GetSession(ctx, s.ID)
	must(t, err)
	if got.UserID != u || got.IP != "1.2.3.4" || !got.LastSeenAt.Equal(t0.Add(time.Minute)) ||
		!got.ExpiresAt.Equal(t0.Add(2*time.Hour)) || string(got.CSRFSecret) != "c" {
		t.Fatalf("session %+v", got)
	}

	n, err := db.DeleteExpiredSessions(ctx, t0.Add(time.Minute))
	must(t, err)
	if n != 1 {
		t.Fatalf("expired deleted %d", n)
	}
	_, err = db.GetSession(ctx, s2.ID)
	wantNotFound(t, err)

	must(t, db.DeleteSession(ctx, s.ID))
	_, err = db.GetSession(ctx, s.ID)
	wantNotFound(t, err)

	must(t, db.CreateSession(ctx, s))
	must(t, db.DeleteUserSessions(ctx, u))
	_, err = db.GetSession(ctx, s.ID)
	wantNotFound(t, err)
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	other := mkUser(t, db, "o@x.io", domain.UserActive)
	id1, err := db.CreateToken(ctx, domain.Token{UserID: u, Name: "ci", Prefix: "ds_ab", Hash: []byte("t1"), CreatedAt: t0})
	must(t, err)
	id2, err := db.CreateToken(ctx, domain.Token{UserID: u, Name: "cli", Prefix: "ds_cd", Hash: []byte("t2"),
		CreatedAt: t0.Add(time.Second), ExpiresAt: ptr(t0.Add(time.Hour))})
	must(t, err)

	wantNotFound(t, db.RevokeToken(ctx, other, id1, t0))
	must(t, db.RevokeToken(ctx, u, id1, t0.Add(time.Minute)))
	must(t, db.RevokeToken(ctx, u, id1, t0.Add(time.Hour)))
	must(t, db.TouchToken(ctx, id2, t0.Add(2*time.Minute)))

	list, err := db.ListTokens(ctx, u)
	must(t, err)
	if len(list) != 2 || list[0].ID != id2 || list[1].ID != id1 {
		t.Fatalf("order %+v", list)
	}
	if list[1].RevokedAt == nil || !list[1].RevokedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("revocation time must be kept: %+v", list[1])
	}
	if list[0].LastUsedAt == nil || !list[0].LastUsedAt.Equal(t0.Add(2*time.Minute)) || list[0].ExpiresAt == nil {
		t.Fatalf("touch/expiry: %+v", list[0])
	}
	got, err := db.GetTokenByHash(ctx, []byte("t2"))
	must(t, err)
	if got.ID != id2 || got.Prefix != "ds_cd" {
		t.Fatalf("by hash %+v", got)
	}
	_, err = db.GetTokenByHash(ctx, []byte("nope"))
	wantNotFound(t, err)
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	must(t, db.SetSetting(ctx, "a", "1"))
	must(t, db.SetSetting(ctx, "a", "2"))
	must(t, db.SetSetting(ctx, "b", "3"))
	must(t, db.DeleteSetting(ctx, "b"))
	must(t, db.DeleteSetting(ctx, "missing"))
	got, err := db.GetSettings(ctx)
	must(t, err)
	if len(got) != 1 || got["a"] != "2" {
		t.Fatalf("settings %v", got)
	}
}
