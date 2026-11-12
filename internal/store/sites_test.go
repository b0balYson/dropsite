package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestCreateAndGetSite(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo", t0)

	s, err := db.GetSite(ctx, "aaaaaaaaaa")
	must(t, err)
	if s.OwnerID != u || s.Title != "Site aaaaaaaaaa" || s.CurrentDeployID != nil || s.DeletedAt != nil || !s.CreatedAt.Equal(t0) {
		t.Fatalf("site %+v", s)
	}
	_, err = db.GetSite(ctx, "missing")
	wantNotFound(t, err)

	got, err := db.FindSiteBySlug(ctx, "promo")
	must(t, err)
	if got.ID != "aaaaaaaaaa" {
		t.Fatalf("by slug %+v", got)
	}
	_, err = db.FindSiteBySlug(ctx, "prom")
	wantNotFound(t, err)
	// The default label is not a slug.
	_, err = db.FindSiteBySlug(ctx, "aaaaaaaaaa")
	wantNotFound(t, err)

	hosts, err := db.ListHostnames(ctx, "aaaaaaaaaa")
	must(t, err)
	if len(hosts) != 2 || hosts[0].Kind != domain.HostDefault || hosts[1].Hostname != "promo."+base || !hosts[1].Verified {
		t.Fatalf("hosts %+v", hosts)
	}

	ok, err := db.SiteIDExists(ctx, "aaaaaaaaaa")
	must(t, err)
	n, err := db.CountActiveSites(ctx, u)
	must(t, err)
	if !ok || n != 1 {
		t.Fatalf("exists %v count %d", ok, n)
	}
}

func TestCreateSiteSlugConflicts(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo", t0)
	mkSite(t, db, "dddddddddd", u, "", t0)
	_, err := db.SoftDeleteSite(ctx, "dddddddddd", t0)
	must(t, err)

	wantCode(t, createSite(db, "bbbbbbbbbb", u, "promo", t0), domain.CodeSlugTaken)
	wantCode(t, createSite(db, "bbbbbbbbbb", u, "aaaaaaaaaa", t0), domain.CodeSlugTaken)
	// Equal to the ID of a deleted site: still taken (its default address is never reissued).
	wantCode(t, createSite(db, "bbbbbbbbbb", u, "dddddddddd", t0), domain.CodeSlugTaken)

	// Failed creations leave nothing behind.
	ok, err := db.SiteIDExists(ctx, "bbbbbbbbbb")
	must(t, err)
	if ok {
		t.Fatal("failed CreateSite must roll back")
	}
	if err := createSite(db, "aaaaaaaaaa", u, "", t0); err == nil {
		t.Fatal("duplicate site ID must fail")
	}
}

func TestSetSlugHost(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "old-name", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "taken", t0)
	later := t0.Add(time.Hour)

	h := host("new-name."+base, "", domain.HostSlug)
	removed, err := db.SetSlugHost(ctx, "aaaaaaaaaa", &h, later)
	must(t, err)
	if removed != "old-name."+base {
		t.Fatalf("removed %q", removed)
	}
	_, err = db.FindSiteBySlug(ctx, "old-name")
	wantNotFound(t, err)
	s, err := db.FindSiteBySlug(ctx, "new-name")
	must(t, err)
	if s.ID != "aaaaaaaaaa" || !s.UpdatedAt.Equal(later) {
		t.Fatalf("by new slug %+v", s)
	}

	taken := host("taken."+base, "", domain.HostSlug)
	_, err = db.SetSlugHost(ctx, "aaaaaaaaaa", &taken, later)
	wantCode(t, err, domain.CodeSlugTaken)
	idLabel := host("bbbbbbbbbb.other.test", "", domain.HostSlug)
	_, err = db.SetSlugHost(ctx, "aaaaaaaaaa", &idLabel, later)
	wantCode(t, err, domain.CodeSlugTaken)
	// A failed replacement keeps the current slug.
	if _, err := db.FindSiteBySlug(ctx, "new-name"); err != nil {
		t.Fatalf("slug lost after failed change: %v", err)
	}

	removed, err = db.SetSlugHost(ctx, "aaaaaaaaaa", nil, later)
	must(t, err)
	if removed != "new-name."+base {
		t.Fatalf("removed %q", removed)
	}
	removed, err = db.SetSlugHost(ctx, "aaaaaaaaaa", nil, later)
	must(t, err)
	if removed != "" {
		t.Fatalf("nothing to remove, got %q", removed)
	}
	_, err = db.SetSlugHost(ctx, "missing", nil, later)
	wantNotFound(t, err)
}

func TestApexHost(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)
	apex := host(base, "", domain.HostApex)

	prev, err := db.SetApexHost(ctx, "aaaaaaaaaa", apex, t0)
	must(t, err)
	if prev != "" {
		t.Fatalf("prev %q", prev)
	}
	prev, err = db.SetApexHost(ctx, "bbbbbbbbbb", apex, t0)
	must(t, err)
	if prev != "aaaaaaaaaa" {
		t.Fatalf("prev %q", prev)
	}
	var n int
	must(t, db.Reader().QueryRowContext(ctx, "SELECT count(*) FROM hostnames WHERE kind = 'apex'").Scan(&n))
	if n != 1 {
		t.Fatalf("apex rows %d", n)
	}
	hosts, err := db.ListHostnames(ctx, "bbbbbbbbbb")
	must(t, err)
	if len(hosts) != 2 || hosts[1].Hostname != base || hosts[1].Kind != domain.HostApex {
		t.Fatalf("hosts %+v", hosts)
	}

	removed, err := db.ClearApexHost(ctx, "aaaaaaaaaa", t0)
	must(t, err)
	if removed != "" {
		t.Fatalf("site A no longer holds apex, removed %q", removed)
	}
	removed, err = db.ClearApexHost(ctx, "bbbbbbbbbb", t0)
	must(t, err)
	if removed != base {
		t.Fatalf("removed %q", removed)
	}
	_, err = db.SetApexHost(ctx, "missing", apex, t0)
	wantNotFound(t, err)
	_, err = db.ClearApexHost(ctx, "missing", t0)
	wantNotFound(t, err)
	// An apex equal to another site's hostname is a conflict.
	_, err = db.SetApexHost(ctx, "aaaaaaaaaa", host("bbbbbbbbbb."+base, "", domain.HostApex), t0)
	wantCode(t, err, domain.CodeSlugTaken)
}

func TestSoftDeleteSite(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	v := mkUser(t, db, "v@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo-may", t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d1", u, 10, t0)
	_, err := db.SetApexHost(ctx, "aaaaaaaaaa", host(base, "", domain.HostApex), t0)
	must(t, err)

	removed, err := db.SoftDeleteSite(ctx, "aaaaaaaaaa", t0.Add(time.Hour))
	must(t, err)
	slices.Sort(removed)
	if want := []string{"aaaaaaaaaa." + base, base, "promo-may." + base}; !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want %v", removed, want)
	}
	_, err = db.SoftDeleteSite(ctx, "aaaaaaaaaa", t0)
	wantNotFound(t, err)

	// Invisible everywhere except the purge/tombstone methods.
	_, err = db.GetSite(ctx, "aaaaaaaaaa")
	wantNotFound(t, err)
	_, err = db.FindSiteBySlug(ctx, "promo-may")
	wantNotFound(t, err)
	wantNotFound(t, db.UpdateSiteTitle(ctx, "aaaaaaaaaa", "x", t0))
	wantNotFound(t, db.TransferSite(ctx, "aaaaaaaaaa", v, t0))
	_, err = db.SetSlugHost(ctx, "aaaaaaaaaa", nil, t0)
	wantNotFound(t, err)
	_, err = db.GetDeploy(ctx, "aaaaaaaaaa", "d1")
	wantNotFound(t, err)
	wantNotFound(t, db.SetCurrentDeploy(ctx, "aaaaaaaaaa", "d1", t0))
	deploys, err := db.ListDeploys(ctx, "aaaaaaaaaa")
	must(t, err)
	hosts, err := db.ListHostnames(ctx, "aaaaaaaaaa")
	must(t, err)
	n, err := db.CountActiveSites(ctx, u)
	must(t, err)
	list, _, err := db.ListSites(ctx, SiteFilter{})
	must(t, err)
	layout, err := db.ActiveLayout(ctx)
	must(t, err)
	if len(deploys)+len(hosts)+n+len(list)+len(layout) != 0 {
		t.Fatalf("deleted site still visible: %d %d %d %d %d", len(deploys), len(hosts), n, len(list), len(layout))
	}
	exists, err := db.SiteIDExists(ctx, "aaaaaaaaaa")
	must(t, err)
	if !exists {
		t.Fatal("tombstone must keep the ID")
	}

	// Slug and apex are free immediately.
	mkSite(t, db, "bbbbbbbbbb", v, "promo-may", t0)
	_, err = db.SetApexHost(ctx, "bbbbbbbbbb", host(base, "", domain.HostApex), t0)
	must(t, err)
}

func TestPurge(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)
	mkSite(t, db, "cccccccccc", u, "", t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d1", u, 1, t0)
	_, err := db.SoftDeleteSite(ctx, "aaaaaaaaaa", t0)
	must(t, err)
	_, err = db.SoftDeleteSite(ctx, "bbbbbbbbbb", t0.Add(48*time.Hour))
	must(t, err)

	ids, err := db.ListSitesToPurge(ctx, t0.Add(24*time.Hour))
	must(t, err)
	if !slices.Equal(ids, []string{"aaaaaaaaaa"}) {
		t.Fatalf("to purge %v", ids)
	}
	must(t, db.MarkSitePurged(ctx, "aaaaaaaaaa", t0.Add(25*time.Hour)))
	wantNotFound(t, db.MarkSitePurged(ctx, "cccccccccc", t0)) // active sites cannot be purged

	var deploys int
	var purged *int64
	must(t, db.Reader().QueryRowContext(ctx, "SELECT count(*) FROM deploys WHERE site_id = 'aaaaaaaaaa'").Scan(&deploys))
	must(t, db.Reader().QueryRowContext(ctx, "SELECT purged_at FROM sites WHERE id = 'aaaaaaaaaa'").Scan(&purged))
	if deploys != 0 || purged == nil {
		t.Fatalf("deploys %d purged %v", deploys, purged)
	}
	ids, err = db.ListSitesToPurge(ctx, t0.Add(100*time.Hour))
	must(t, err)
	if !slices.Equal(ids, []string{"bbbbbbbbbb"}) {
		t.Fatalf("to purge after purge %v", ids)
	}
}

func TestUpdateAndTransferSite(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	v := mkUser(t, db, "v@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	must(t, db.UpdateSiteTitle(ctx, "aaaaaaaaaa", "New", t0.Add(time.Hour)))
	must(t, db.TransferSite(ctx, "aaaaaaaaaa", v, t0.Add(2*time.Hour)))
	s, err := db.GetSite(ctx, "aaaaaaaaaa")
	must(t, err)
	if s.Title != "New" || s.OwnerID != v || !s.UpdatedAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("site %+v", s)
	}
}

func TestListSites(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "owner@x.io", domain.UserActive)
	v := mkUser(t, db, "other@y.io", domain.UserActive)
	mkSite(t, db, "site000001", u, "promo-may", t0)
	mkSite(t, db, "site000002", u, "", t0.Add(time.Minute))
	mkSite(t, db, "site000003", v, "", t0.Add(2*time.Minute))
	mkSite(t, db, "site000004", v, "", t0.Add(2*time.Minute))
	must(t, db.UpdateSiteTitle(ctx, "site000002", "100% off", t0.Add(time.Minute)))
	mkDeploy(t, db, "site000001", "d1", u, 111, t0.Add(3*time.Minute))
	mkDeploy(t, db, "site000001", "d2", u, 222, t0.Add(4*time.Minute))

	all, total, err := db.ListSites(ctx, SiteFilter{})
	must(t, err)
	ids := make([]string, len(all))
	for i, s := range all {
		ids[i] = s.ID
	}
	if total != 4 || !slices.Equal(ids, []string{"site000001", "site000003", "site000004", "site000002"}) {
		t.Fatalf("total %d order %v", total, ids)
	}
	first := all[0]
	if first.OwnerEmail != "owner@x.io" || first.SizeBytes != 222 || first.LastDeployAt == nil ||
		!first.LastDeployAt.Equal(t0.Add(4*time.Minute)) || len(first.Hostnames) != 2 || first.Hostnames[1].Kind != domain.HostSlug {
		t.Fatalf("summary %+v", first)
	}
	if all[1].LastDeployAt != nil || all[1].SizeBytes != 0 || len(all[1].Hostnames) != 1 {
		t.Fatalf("no-deploy summary %+v", all[1])
	}

	page, total, err := db.ListSites(ctx, SiteFilter{Page: Page{Limit: 2, Offset: 2}})
	must(t, err)
	if total != 4 || len(page) != 2 || page[0].ID != "site000004" {
		t.Fatalf("page total %d %+v", total, page)
	}

	mine, total, err := db.ListSites(ctx, SiteFilter{OwnerID: ptr(v)})
	must(t, err)
	if total != 2 || len(mine) != 2 || mine[0].OwnerID != v {
		t.Fatalf("owner filter %d %+v", total, mine)
	}

	queries := map[string][]string{
		"100%":      {"site000002"},               // literal %, not a wildcard
		"%":         {"site000002"},               // only one title contains a percent sign
		"promo-may": {"site000001"},               // hostname
		"y.io":      {"site000003", "site000004"}, // owner email
		"SITE00000": {"site000001", "site000003", "site000004", "site000002"},
		"site_0":    {}, // literal underscore
	}
	for q, want := range queries {
		got, total, err := db.ListSites(ctx, SiteFilter{Query: q})
		must(t, err)
		gotIDs := []string{}
		for _, s := range got {
			gotIDs = append(gotIDs, s.ID)
		}
		if total != len(want) || !slices.Equal(gotIDs, want) {
			t.Errorf("query %q: total %d ids %v, want %v", q, total, gotIDs, want)
		}
	}
}

func TestActiveLayout(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)
	mkSite(t, db, "cccccccccc", u, "", t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d1", u, 1, t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d2", u, 1, t0.Add(time.Second))
	mkDeploy(t, db, "aaaaaaaaaa", "d3", u, 1, t0.Add(2*time.Second))
	mkDeploy(t, db, "cccccccccc", "c1", u, 1, t0)
	must(t, db.MarkDeploysPruned(ctx, "aaaaaaaaaa", []string{"d1"}))
	_, err := db.SoftDeleteSite(ctx, "cccccccccc", t0)
	must(t, err)

	got, err := db.ActiveLayout(ctx)
	must(t, err)
	if len(got) != 2 {
		t.Fatalf("layout %+v", got)
	}
	a, b := got[0], got[1]
	if a.SiteID != "aaaaaaaaaa" || a.CurrentDeployID == nil || *a.CurrentDeployID != "d3" ||
		!slices.Equal(a.Hostnames, []string{"aaaaaaaaaa." + base, "promo." + base}) ||
		!slices.Equal(a.ReadyDeployIDs, []string{"d2", "d3"}) {
		t.Fatalf("site A layout %+v", a)
	}
	if b.SiteID != "bbbbbbbbbb" || b.CurrentDeployID != nil || len(b.ReadyDeployIDs) != 0 || len(b.Hostnames) != 1 {
		t.Fatalf("site B layout %+v", b)
	}
}
