package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// A stage 1 database keeps its addresses active after the external domains migration.
func TestExternalMigrationFromStage1(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", file)
	must(t, err)
	initial, err := migrationFS.ReadFile("migrations/0001_initial.sql")
	must(t, err)
	for _, q := range []string{
		string(initial),
		"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT",
		"INSERT INTO schema_migrations VALUES (1, 0)",
		"INSERT INTO users (id, email, role, status, created_at, updated_at) VALUES (1, 'u@x.io', 'user', 'active', 0, 0)",
		"INSERT INTO sites (id, owner_id, title, created_at, updated_at) VALUES ('aaaaaaaaaa', 1, 't', 0, 0)",
		"INSERT INTO hostnames (hostname, site_id, kind, created_at) VALUES ('aaaaaaaaaa." + base + "', 'aaaaaaaaaa', 'default', 0)",
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(t, raw.Close())

	db, err := Open(ctx, file)
	must(t, err)
	defer db.Close()
	hosts, err := db.ListHostnames(ctx, "aaaaaaaaaa")
	must(t, err)
	if len(hosts) != 1 || hosts[0].Status() != domain.HostActive || hosts[0].ReviewedAt != nil {
		t.Fatalf("hosts %+v", hosts)
	}
	l, err := db.ActiveLayout(ctx)
	must(t, err)
	if len(l) != 1 || len(l[0].Hostnames) != 1 {
		t.Fatalf("layout %+v", l)
	}
}

func extHost(name, siteID string) domain.Hostname {
	return domain.Hostname{Hostname: name, SiteID: siteID, Kind: domain.HostExternal, CreatedAt: t0}
}

func TestAddExternalHost(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)

	must(t, db.AddExternalHost(ctx, extHost("promo.example.ru", "aaaaaaaaaa"), 2))
	hosts, err := db.ListHostnames(ctx, "aaaaaaaaaa")
	must(t, err)
	last := hosts[len(hosts)-1]
	if len(hosts) != 3 || last.Kind != domain.HostExternal || last.Verified || last.Status() != domain.HostPending {
		t.Fatalf("hosts %+v", hosts)
	}

	// Taken by any site, in any status, and by any other kind of address.
	wantCode(t, db.AddExternalHost(ctx, extHost("promo.example.ru", "bbbbbbbbbb"), 2), domain.CodeDomainTaken)
	wantCode(t, db.AddExternalHost(ctx, extHost("promo.example.ru", "aaaaaaaaaa"), 2), domain.CodeDomainTaken)
	wantCode(t, db.AddExternalHost(ctx, extHost("promo."+base, "bbbbbbbbbb"), 2), domain.CodeDomainTaken)

	// Limit counts external hosts of the site only.
	must(t, db.AddExternalHost(ctx, extHost("two.example.ru", "aaaaaaaaaa"), 2))
	wantCode(t, db.AddExternalHost(ctx, extHost("three.example.ru", "aaaaaaaaaa"), 2), domain.CodeDomainLimit)
	must(t, db.AddExternalHost(ctx, extHost("three.example.ru", "bbbbbbbbbb"), 2))

	wantNotFound(t, db.AddExternalHost(ctx, extHost("x.example.ru", "missing000"), 2))
	_, err = db.SoftDeleteSite(ctx, "bbbbbbbbbb", t0)
	must(t, err)
	wantNotFound(t, db.AddExternalHost(ctx, extHost("x.example.ru", "bbbbbbbbbb"), 2))
	// The deleted site released its domain.
	must(t, db.AddExternalHost(ctx, extHost("three.example.ru", "aaaaaaaaaa"), 3))
}

func TestReviewAndLayout(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	admin := mkUser(t, db, "a@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	must(t, db.AddExternalHost(ctx, extHost("promo.example.ru", "aaaaaaaaaa"), 5))
	must(t, db.AddExternalHost(ctx, extHost("example.ru", "aaaaaaaaaa"), 5))

	layoutHosts := func() []string {
		t.Helper()
		l, err := db.ActiveLayout(ctx)
		must(t, err)
		return l[0].Hostnames
	}
	if got := layoutHosts(); !slices.Equal(got, []string{"aaaaaaaaaa." + base}) {
		t.Fatalf("pending hosts must not be published: %v", got)
	}

	at := t0.Add(time.Second)
	h, err := db.ReviewExternalHost(ctx, ExternalReview{Hostname: "promo.example.ru", SiteID: "aaaaaaaaaa", Approve: true, ReviewerID: admin, At: at})
	must(t, err)
	if h.Status() != domain.HostActive || h.ReviewedBy == nil || *h.ReviewedBy != admin || !h.ReviewedAt.Equal(at) {
		t.Fatalf("approved %+v", h)
	}
	if got := layoutHosts(); !slices.Equal(got, []string{"aaaaaaaaaa." + base, "promo.example.ru"}) {
		t.Fatalf("layout %v", got)
	}

	h, err = db.ReviewExternalHost(ctx, ExternalReview{Hostname: "promo.example.ru", SiteID: "aaaaaaaaaa", Reason: "not ours", ReviewerID: admin, At: at})
	must(t, err)
	if h.Status() != domain.HostRejected || h.RejectReason != "not ours" {
		t.Fatalf("rejected %+v", h)
	}
	if got := layoutHosts(); len(got) != 1 {
		t.Fatalf("rejected host published: %v", got)
	}
	// Approving again clears the reason.
	h, err = db.ReviewExternalHost(ctx, ExternalReview{Hostname: "promo.example.ru", SiteID: "aaaaaaaaaa", Approve: true, ReviewerID: admin, At: at})
	must(t, err)
	if h.RejectReason != "" || !h.Verified {
		t.Fatalf("re-approved %+v", h)
	}

	// Wrong site, non-external host, missing host → not found.
	_, err = db.ReviewExternalHost(ctx, ExternalReview{Hostname: "promo.example.ru", SiteID: "bbbbbbbbbb", Approve: true, At: at})
	wantNotFound(t, err)
	_, err = db.ReviewExternalHost(ctx, ExternalReview{Hostname: "aaaaaaaaaa." + base, SiteID: "aaaaaaaaaa", At: at})
	wantNotFound(t, err)
	_, err = db.ReviewExternalHost(ctx, ExternalReview{Hostname: "nope.example.ru", SiteID: "aaaaaaaaaa", At: at})
	wantNotFound(t, err)
}

func TestRemoveExternalHost(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "promo", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)
	must(t, db.AddExternalHost(ctx, extHost("promo.example.ru", "aaaaaaaaaa"), 5))

	_, err := db.RemoveExternalHost(ctx, "bbbbbbbbbb", "promo.example.ru", t0)
	wantNotFound(t, err)
	_, err = db.RemoveExternalHost(ctx, "aaaaaaaaaa", "promo."+base, t0)
	wantNotFound(t, err)
	_, err = db.RemoveExternalHost(ctx, "aaaaaaaaaa", "aaaaaaaaaa."+base, t0)
	wantNotFound(t, err)

	h, err := db.RemoveExternalHost(ctx, "aaaaaaaaaa", "promo.example.ru", t0)
	must(t, err)
	if h.Hostname != "promo.example.ru" || h.Kind != domain.HostExternal {
		t.Fatalf("removed %+v", h)
	}
	hosts, err := db.ListHostnames(ctx, "aaaaaaaaaa")
	must(t, err)
	if len(hosts) != 2 {
		t.Fatalf("hosts %+v", hosts)
	}
	// Free again for another site.
	must(t, db.AddExternalHost(ctx, extHost("promo.example.ru", "bbbbbbbbbb"), 5))
}

func TestListAndGetExternalHosts(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "owner@x.io", domain.UserActive)
	v := mkUser(t, db, "other@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	mkSite(t, db, "bbbbbbbbbb", v, "", t0)
	for i := range 3 {
		h := extHost(fmt.Sprintf("p%d.example.ru", i), "aaaaaaaaaa")
		h.CreatedAt = t0.Add(time.Duration(i) * time.Minute)
		must(t, db.AddExternalHost(ctx, h, 5))
	}
	must(t, db.AddExternalHost(ctx, extHost("shop.other.org", "bbbbbbbbbb"), 5))
	_, err := db.ReviewExternalHost(ctx, ExternalReview{Hostname: "p1.example.ru", SiteID: "aaaaaaaaaa", Approve: true, ReviewerID: u, At: t0})
	must(t, err)
	_, err = db.ReviewExternalHost(ctx, ExternalReview{Hostname: "p2.example.ru", SiteID: "aaaaaaaaaa", Reason: "no", ReviewerID: u, At: t0})
	must(t, err)

	list := func(f ExternalHostFilter) ([]string, int) {
		t.Helper()
		rows, total, err := db.ListExternalHosts(ctx, f)
		must(t, err)
		var names []string
		for _, r := range rows {
			names = append(names, r.Hostname.Hostname)
		}
		return names, total
	}
	if names, total := list(ExternalHostFilter{Status: domain.HostPending}); total != 2 ||
		!slices.Equal(names, []string{"p0.example.ru", "shop.other.org"}) {
		t.Fatalf("pending %v %d", names, total)
	}
	if names, _ := list(ExternalHostFilter{Status: domain.HostActive}); !slices.Equal(names, []string{"p1.example.ru"}) {
		t.Fatalf("active %v", names)
	}
	if names, _ := list(ExternalHostFilter{Status: domain.HostRejected}); !slices.Equal(names, []string{"p2.example.ru"}) {
		t.Fatalf("rejected %v", names)
	}
	if _, total := list(ExternalHostFilter{}); total != 4 {
		t.Fatalf("all %d", total)
	}
	if names, _ := list(ExternalHostFilter{Query: "other@"}); !slices.Equal(names, []string{"shop.other.org"}) {
		t.Fatalf("by owner %v", names)
	}
	if names, total := list(ExternalHostFilter{Page: Page{Limit: 1, Offset: 1}}); total != 4 || len(names) != 1 {
		t.Fatalf("page %v %d", names, total)
	}

	got, err := db.GetExternalHost(ctx, "shop.other.org")
	must(t, err)
	if got.OwnerEmail != "other@x.io" || got.OwnerID != v || got.SiteTitle != "Site bbbbbbbbbb" || got.Hostname.SiteID != "bbbbbbbbbb" {
		t.Fatalf("get %+v", got)
	}
	_, err = db.GetExternalHost(ctx, "aaaaaaaaaa."+base)
	wantNotFound(t, err)
	_, err = db.SoftDeleteSite(ctx, "bbbbbbbbbb", t0)
	must(t, err)
	_, err = db.GetExternalHost(ctx, "shop.other.org")
	wantNotFound(t, err)
	if _, total := list(ExternalHostFilter{}); total != 3 {
		t.Fatalf("deleted site's domains listed: %d", total)
	}
}
