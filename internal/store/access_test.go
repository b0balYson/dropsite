package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSiteAccessFields(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)

	s, err := db.GetSite(ctx, "aaaaaaaaaa")
	must(t, err)
	if s.Noindex || s.Protected || s.ExpiresAt != nil {
		t.Fatalf("new site has access fields: %+v", s)
	}
	acc, err := db.GetSiteAccess(ctx, "aaaaaaaaaa")
	must(t, err)
	if acc.PasswordHash != "" || acc.Version != 0 {
		t.Fatalf("access %+v", acc)
	}

	t1 := t0.Add(time.Hour)
	must(t, db.SetSiteNoindex(ctx, "aaaaaaaaaa", true, t1))
	v, err := db.SetSitePassword(ctx, "aaaaaaaaaa", "$argon2id$hash", t1)
	must(t, err)
	if v != 1 {
		t.Fatalf("version %d", v)
	}
	exp := t0.Add(48 * time.Hour)
	must(t, db.SetSiteExpiry(ctx, "aaaaaaaaaa", &exp, t1))

	s, err = db.GetSite(ctx, "aaaaaaaaaa")
	must(t, err)
	if !s.Noindex || !s.Protected || s.ExpiresAt == nil || !s.ExpiresAt.Equal(exp) || !s.UpdatedAt.Equal(t1) {
		t.Fatalf("site %+v", s)
	}
	byHost, err := db.FindSiteByHostname(ctx, "promo."+base)
	must(t, err)
	if byHost.ID != "aaaaaaaaaa" || !byHost.Protected || !byHost.Noindex {
		t.Fatalf("by host %+v", byHost)
	}
	_, err = db.FindSiteByHostname(ctx, "nope."+base)
	wantNotFound(t, err)

	// Removing the password opens the site and still bumps the version.
	v, err = db.SetSitePassword(ctx, "aaaaaaaaaa", "", t1)
	must(t, err)
	acc, err = db.GetSiteAccess(ctx, "aaaaaaaaaa")
	must(t, err)
	if v != 2 || acc.Version != 2 || acc.PasswordHash != "" {
		t.Fatalf("after clear: v=%d %+v", v, acc)
	}
	must(t, db.SetSiteExpiry(ctx, "aaaaaaaaaa", nil, t1))

	// Layout and list carry the flags.
	must(t, db.SetSiteNoindex(ctx, "bbbbbbbbbb", true, t1))
	_, err = db.SetSitePassword(ctx, "bbbbbbbbbb", "h", t1)
	must(t, err)
	layout, err := db.ActiveLayout(ctx)
	must(t, err)
	flags := map[string][2]bool{}
	for _, l := range layout {
		flags[l.SiteID] = [2]bool{l.Noindex, l.Protected}
	}
	if flags["aaaaaaaaaa"] != [2]bool{true, false} || flags["bbbbbbbbbb"] != [2]bool{true, true} {
		t.Fatalf("layout flags %v", flags)
	}
	rows, _, err := db.ListSites(ctx, SiteFilter{})
	must(t, err)
	for _, r := range rows {
		if r.ID == "bbbbbbbbbb" && (!r.Protected || !r.Noindex) {
			t.Fatalf("list row %+v", r)
		}
	}

	// Deleted sites are invisible to every access method.
	_, err = db.SoftDeleteSite(ctx, "bbbbbbbbbb", t1)
	must(t, err)
	wantNotFound(t, db.SetSiteNoindex(ctx, "bbbbbbbbbb", false, t1))
	_, err = db.SetSitePassword(ctx, "bbbbbbbbbb", "", t1)
	wantNotFound(t, err)
	_, err = db.GetSiteAccess(ctx, "bbbbbbbbbb")
	wantNotFound(t, err)
	wantNotFound(t, db.SetSiteExpiry(ctx, "bbbbbbbbbb", nil, t1))
	_, err = db.FindSiteByHostname(ctx, "bbbbbbbbbb."+base)
	wantNotFound(t, err)
	must(t, db.MarkSitePurged(ctx, "bbbbbbbbbb", t1))
	var hash string
	must(t, db.Reader().QueryRowContext(ctx, "SELECT access_password_hash FROM sites WHERE id = 'bbbbbbbbbb'").Scan(&hash))
	if hash != "" {
		t.Fatal("purge kept the password hash")
	}
}

func TestListExpiredSites(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	for _, id := range []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd"} {
		mkSite(t, db, id, u, "", t0)
	}
	past, now, future := t0.Add(time.Minute), t0.Add(time.Hour), t0.Add(2*time.Hour)
	must(t, db.SetSiteExpiry(ctx, "aaaaaaaaaa", &past, t0))
	must(t, db.SetSiteExpiry(ctx, "bbbbbbbbbb", &now, t0))
	must(t, db.SetSiteExpiry(ctx, "cccccccccc", &future, t0))
	must(t, db.SetSiteExpiry(ctx, "dddddddddd", &past, t0))
	_, err := db.SoftDeleteSite(ctx, "dddddddddd", t0)
	must(t, err)

	ids, err := db.ListExpiredSites(ctx, now)
	must(t, err)
	if len(ids) != 2 || ids[0] != "aaaaaaaaaa" || ids[1] != "bbbbbbbbbb" {
		t.Fatalf("expired %v", ids)
	}
}

func TestUserLimits(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)

	o, err := db.GetUserLimits(ctx, u)
	must(t, err)
	if !o.Empty() {
		t.Fatalf("fresh user has overrides %+v", o)
	}
	want := domain.LimitOverrides{MaxSiteBytes: ptr[int64](100 << 20), MaxSitesPerUser: ptr[int64](20)}
	must(t, db.SetUserLimits(ctx, u, want, t0))
	o, err = db.GetUserLimits(ctx, u)
	must(t, err)
	if o.MaxSiteBytes == nil || *o.MaxSiteBytes != 100<<20 || o.MaxSitesPerUser == nil || *o.MaxSitesPerUser != 20 ||
		o.MaxFileBytes != nil || o.MaxFiles != nil || o.KeepReleases != nil {
		t.Fatalf("overrides %+v", o)
	}
	// Replace: fields not given are cleared.
	must(t, db.SetUserLimits(ctx, u, domain.LimitOverrides{KeepReleases: ptr[int64](9)}, t0))
	o, err = db.GetUserLimits(ctx, u)
	must(t, err)
	if o.MaxSiteBytes != nil || o.KeepReleases == nil || *o.KeepReleases != 9 {
		t.Fatalf("after replace %+v", o)
	}
	must(t, db.SetUserLimits(ctx, u, domain.LimitOverrides{}, t0))
	var n int
	must(t, db.Reader().QueryRowContext(ctx, "SELECT count(*) FROM user_limits").Scan(&n))
	if n != 0 {
		t.Fatal("empty overrides left a row")
	}
	wantNotFound(t, db.SetUserLimits(ctx, 9999, want, t0))
	// The schema refuses nonsense even if the service did not.
	if err := db.SetUserLimits(ctx, u, domain.LimitOverrides{KeepReleases: ptr[int64](1)}, t0); err == nil {
		t.Fatal("keep_releases 1 accepted")
	}
}

func TestEnsureSecret(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	first, err := db.EnsureSecret(ctx, "visitor", []byte("one"), t0)
	must(t, err)
	second, err := db.EnsureSecret(ctx, "visitor", []byte("two"), t0)
	must(t, err)
	if !bytes.Equal(first, []byte("one")) || !bytes.Equal(second, first) {
		t.Fatalf("secret changed: %q %q", first, second)
	}
	other, err := db.EnsureSecret(ctx, "other", []byte("x"), t0)
	must(t, err)
	if string(other) != "x" {
		t.Fatalf("other %q", other)
	}
}

func TestAccessClosedDatabase(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	must(t, db.Close())
	calls := []func() error{
		func() error { _, err := db.FindSiteByHostname(ctx, "x"); return err },
		func() error { return db.SetSiteNoindex(ctx, "s", true, t0) },
		func() error { _, err := db.GetSiteAccess(ctx, "s"); return err },
		func() error { _, err := db.SetSitePassword(ctx, "s", "", t0); return err },
		func() error { return db.SetSiteExpiry(ctx, "s", nil, t0) },
		func() error { _, err := db.ListExpiredSites(ctx, t0); return err },
		func() error { _, err := db.GetUserLimits(ctx, 1); return err },
		func() error { return db.SetUserLimits(ctx, 1, domain.LimitOverrides{}, t0) },
		func() error { _, err := db.EnsureSecret(ctx, "n", nil, t0); return err },
	}
	for i, c := range calls {
		if err := c(); err == nil || errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}
