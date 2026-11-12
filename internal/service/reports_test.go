package service

import (
	"context"
	"slices"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

const brokenPage = `<!doctype html><html lang="en"><head><meta name="viewport" content="width=device-width">
<meta name="description" content="d"><title>t</title></head><body><a href="/abuot">x</a>
<img src="hero.jpg" alt="h"></body></html>`

func TestPublishStoresQualityReport(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	other := e.user("bob@pp.test")
	if _, err := e.svc.CreateSite(ctx, u, "p", "promo"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetDeployReport(ctx, u, "promo", ""); domain.AsError(err).Code != domain.CodeSiteNotPublished {
		t.Fatalf("unpublished: %v", err)
	}
	res, err := e.svc.Deploy(ctx, u, "promo", zipOf(t, map[string]string{"index.html": brokenPage}), domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	q := res.Quality
	if q == nil || q.Counts.BrokenLinks != 1 || q.Counts.MissingAssets != 1 || q.Score != 90 {
		t.Fatalf("result report %+v", q)
	}
	// Optimization ran: the generated favicon counts as present and is reported.
	if q.Optimization == nil || !slices.Contains(q.Optimization.Generated, "favicon.ico") {
		t.Fatalf("optimization %+v", q.Optimization)
	}

	got, err := e.svc.GetDeployReport(ctx, u, "promo", "")
	if err != nil || got.Deploy.ID != res.Deploy.ID || !got.Current || got.Report == nil || got.Report.Score != 90 {
		t.Fatalf("current report %+v %v", got, err)
	}
	_, err = e.svc.GetDeployReport(ctx, other, "promo", res.Deploy.ID)
	wantNotFound(t, err)
	_, err = e.svc.GetDeployReport(ctx, u, "promo", "nope")
	wantNotFound(t, err)

	// Fixing the link through an edit gives a better report.
	up, err := e.svc.UpdateSiteFiles(ctx, u, "promo", res.Deploy.ID, FileChanges{
		Put: []InlineFile{{Path: "abuot.html", Content: brokenPage}, {Path: "hero.jpg", Content: "jpg"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Quality == nil || up.Quality.Counts.BrokenLinks != 0 || up.Quality.Counts.MissingAssets != 0 {
		t.Fatalf("edit report %+v", up.Quality)
	}

	// A counter re-release keeps the report; rollback returns the target's.
	if _, err := e.svc.SetSiteCounters(ctx, u, "promo", domain.Counters{MetrikaID: "12345"}, domain.SourceAPI); err != nil {
		t.Fatal(err)
	}
	cur, err := e.svc.GetDeployReport(ctx, u, "promo", "")
	if err != nil || cur.Deploy.ID == up.Deploy.ID || cur.Report == nil || cur.Report.Score != up.Quality.Score {
		t.Fatalf("re-release report %+v %v", cur, err)
	}
	rb, err := e.svc.Rollback(ctx, u, "promo", res.Deploy.ID)
	if err != nil || rb.Quality == nil || rb.Quality.Counts.BrokenLinks != 1 {
		t.Fatalf("rollback report %+v %v", rb.Quality, err)
	}
	scores, err := e.svc.DeployScores(ctx, u, "promo")
	if err != nil || len(scores) != 3 || scores[res.Deploy.ID].Grade != "A" {
		t.Fatalf("scores %v %v", scores, err)
	}
	_, err = e.svc.DeployScores(ctx, other, "promo")
	wantNotFound(t, err)
}

func TestPrunedReleasesLoseReports(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	if _, err := e.svc.CreateSite(ctx, u, "p", "promo"); err != nil {
		t.Fatal(err)
	}
	var first string
	for i := range 7 {
		d := deploy(t, e, u, "promo", brokenPage)
		if i == 0 {
			first = d.Deploy.ID
		}
		e.clock.Advance(1e9)
	}
	got, err := e.svc.GetDeployReport(ctx, u, "promo", first)
	if err != nil || got.Deploy.Status != domain.DeployPruned || got.Report != nil {
		t.Fatalf("pruned %+v %v", got, err)
	}
}
