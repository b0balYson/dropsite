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
	must(t, db.PutDeployReport(ctx, "pubset0001", "d1", []byte(`{"score":80,"grade":"B"}`), t0))
	if _, err := db.SoftDeleteSite(ctx, "pubset0001", t0); err != nil {
		t.Fatal(err)
	}
	must(t, db.MarkSitePurged(ctx, "pubset0001", t0))
	var n int
	if err := db.Writer().QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM site_publish_settings) + "+
		"(SELECT COUNT(*) FROM deploy_reports)").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after purge %d %v", n, err)
	}
}

func TestDeployReports(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	owner := mkUser(t, db, "a@example.test", domain.UserActive)
	mkSite(t, db, "reports001", owner, "", t0)

	if _, err := db.GetDeployReport(ctx, "reports001", "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing report: %v", err)
	}
	must(t, db.PutDeployReport(ctx, "reports001", "d1", []byte(`{"score":70,"grade":"C"}`), t0))
	must(t, db.PutDeployReport(ctx, "reports001", "d1", []byte(`{"score":91,"grade":"A"}`), t0))
	must(t, db.PutDeployReport(ctx, "reports001", "d2", []byte(`{"score":60,"grade":"C"}`), t0))
	got, err := db.GetDeployReport(ctx, "reports001", "d1")
	if err != nil || string(got) != `{"score":91,"grade":"A"}` {
		t.Fatalf("get %s %v", got, err)
	}
	scores, err := db.ListDeployScores(ctx, "reports001")
	if err != nil || len(scores) != 2 || scores["d1"] != (domain.QualityScore{Score: 91, Grade: "A"}) {
		t.Fatalf("scores %v %v", scores, err)
	}
	must(t, db.DeleteDeployReports(ctx, "reports001", []string{"d1"}))
	must(t, db.DeleteDeployReports(ctx, "reports001", nil))
	if scores, _ = db.ListDeployScores(ctx, "reports001"); len(scores) != 1 {
		t.Fatalf("after delete %v", scores)
	}
}
