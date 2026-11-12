package store

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSiteDrafts(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner := mkUser(t, db, "o@example.test", domain.UserActive)
	mkSite(t, db, "s1", owner, "", t0)
	mkSite(t, db, "s2", owner, "", t0)
	mkDeploy(t, db, "s1", "d1", owner, 1, t0)
	mkDeploy(t, db, "s1", "d2", owner, 1, t0.Add(time.Minute))

	wantNotFound(t, func() error { _, err := db.GetSiteDraft(ctx, "s1"); return err }())
	prev, err := db.SetSiteDraft(ctx, "s1", "d1", owner, t0)
	if err != nil || prev != nil {
		t.Fatalf("first draft: %v %v", prev, err)
	}
	prev, err = db.SetSiteDraft(ctx, "s1", "d2", owner, t0.Add(time.Hour))
	if err != nil || prev == nil || *prev != "d1" {
		t.Fatalf("replace draft: %v %v", prev, err)
	}
	d, err := db.GetSiteDraft(ctx, "s1")
	if err != nil || d.DeployID == nil || *d.DeployID != "d2" || d.CreatedBy == nil || *d.CreatedBy != owner ||
		!d.CreatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("draft %+v %v", d, err)
	}
	if _, err := db.SetSiteDraft(ctx, "nope", "d2", owner, t0); err == nil {
		t.Fatal("draft of a missing site accepted")
	}

	hosts, err := db.ListDraftHosts(ctx)
	if err != nil || len(hosts) != 1 || hosts["s1"] == nil || *hosts["s1"] != "d2" {
		t.Fatalf("hosts %v %v", hosts, err)
	}
	if err := db.ClearSiteDraft(ctx, "s1", t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearSiteDraft(ctx, "s2", t0); err != nil {
		t.Fatalf("clearing a site without a draft: %v", err)
	}
	d, err = db.GetSiteDraft(ctx, "s1")
	if err != nil || d.DeployID != nil {
		t.Fatalf("cleared draft %+v %v", d, err)
	}
	hosts, _ = db.ListDraftHosts(ctx)
	if v, ok := hosts["s1"]; !ok || v != nil {
		t.Fatalf("the preview address must stay allocated: %v", hosts)
	}
}

func TestSiteDraftOfDeletedSite(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner := mkUser(t, db, "o@example.test", domain.UserActive)
	mkSite(t, db, "s1", owner, "", t0)
	mkDeploy(t, db, "s1", "d1", owner, 1, t0)
	if _, err := db.SetSiteDraft(ctx, "s1", "d1", owner, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SoftDeleteSite(ctx, "s1", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, func() error { _, err := db.GetSiteDraft(ctx, "s1"); return err }())
	if hosts, _ := db.ListDraftHosts(ctx); len(hosts) != 0 {
		t.Fatalf("deleted site listed: %v", hosts)
	}
	if err := db.MarkSitePurged(ctx, "s1", t0.Add(2*time.Hour)); err != nil {
		t.Fatalf("purge with a draft: %v", err)
	}
}
