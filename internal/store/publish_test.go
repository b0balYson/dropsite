package store

import (
	"context"
	"errors"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestPublishSettings(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	owner := mkUser(t, db, "a@example.test", domain.UserActive)
	mkSite(t, db, "pubset0001", owner, "", t0)

	s, err := db.GetPublishSettings(ctx, "pubset0001")
	if err != nil || !s.Optimize || s.SiteID != "pubset0001" {
		t.Fatalf("default %+v %v", s, err)
	}
	must(t, db.SetPublishSettings(ctx, domain.PublishSettings{SiteID: "pubset0001", Optimize: false}))
	if s, _ = db.GetPublishSettings(ctx, "pubset0001"); s.Optimize {
		t.Fatal("optimize still on")
	}
	must(t, db.SetPublishSettings(ctx, domain.PublishSettings{SiteID: "pubset0001", Optimize: true}))
	if s, _ = db.GetPublishSettings(ctx, "pubset0001"); !s.Optimize {
		t.Fatal("optimize still off")
	}
	if err := db.SetPublishSettings(ctx, domain.PublishSettings{SiteID: "missing000"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing site: %v", err)
	}

	must(t, db.SetPublishSettings(ctx, domain.PublishSettings{SiteID: "pubset0001", Optimize: false}))
	if _, err := db.SoftDeleteSite(ctx, "pubset0001", t0); err != nil {
		t.Fatal(err)
	}
	must(t, db.MarkSitePurged(ctx, "pubset0001", t0))
	var n int
	if err := db.Writer().QueryRowContext(ctx, "SELECT COUNT(*) FROM site_publish_settings").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after purge %d %v", n, err)
	}
}
