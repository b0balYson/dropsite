package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestDeploys(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d1", u, 10, t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d2", u, 20, t0.Add(time.Second))
	mkDeploy(t, db, "aaaaaaaaaa", "d3", u, 30, t0.Add(time.Second)) // same time: id breaks the tie
	mkDeploy(t, db, "bbbbbbbbbb", "b1", u, 1, t0)

	list, err := db.ListDeploys(ctx, "aaaaaaaaaa")
	must(t, err)
	ids := []string{}
	for _, d := range list {
		ids = append(ids, d.ID)
	}
	if !slices.Equal(ids, []string{"d3", "d2", "d1"}) {
		t.Fatalf("order %v", ids)
	}
	s, err := db.GetSite(ctx, "aaaaaaaaaa")
	must(t, err)
	if *s.CurrentDeployID != "d3" || !s.UpdatedAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("current %+v", s)
	}

	d, err := db.GetDeploy(ctx, "aaaaaaaaaa", "d2")
	must(t, err)
	if d.SizeBytes != 20 || d.Source != domain.SourceAPI || d.Status != domain.DeployReady || d.CreatedBy != u {
		t.Fatalf("deploy %+v", d)
	}
	_, err = db.GetDeploy(ctx, "bbbbbbbbbb", "d2")
	wantNotFound(t, err)

	must(t, db.SetCurrentDeploy(ctx, "aaaaaaaaaa", "d1", t0.Add(time.Hour)))
	wantNotFound(t, db.SetCurrentDeploy(ctx, "aaaaaaaaaa", "b1", t0)) // deploy of another site
	wantNotFound(t, db.SetCurrentDeploy(ctx, "aaaaaaaaaa", "nope", t0))

	err = db.InsertDeployAndActivate(ctx, domain.Deploy{ID: "x", SiteID: "missing", CreatedBy: u,
		Source: domain.SourceUI, Status: domain.DeployReady, CreatedAt: t0}, t0)
	wantNotFound(t, err)
}

func TestMarkDeploysPrunedKeepsCurrent(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	mkSite(t, db, "aaaaaaaaaa", u, "", t0)
	mkSite(t, db, "bbbbbbbbbb", u, "", t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d1", u, 1, t0)
	mkDeploy(t, db, "aaaaaaaaaa", "d2", u, 1, t0.Add(time.Second))
	mkDeploy(t, db, "bbbbbbbbbb", "b1", u, 1, t0)
	mkDeploy(t, db, "bbbbbbbbbb", "b2", u, 1, t0.Add(time.Second))

	must(t, db.MarkDeploysPruned(ctx, "aaaaaaaaaa", []string{"d1", "d2", "b1"}))
	must(t, db.MarkDeploysPruned(ctx, "aaaaaaaaaa", nil))

	status := func(site, id string) domain.DeployStatus {
		d, err := db.GetDeploy(ctx, site, id)
		must(t, err)
		return d.Status
	}
	if status("aaaaaaaaaa", "d1") != domain.DeployPruned {
		t.Fatal("d1 must be pruned")
	}
	if status("aaaaaaaaaa", "d2") != domain.DeployReady {
		t.Fatal("current deploy must never be pruned")
	}
	if status("bbbbbbbbbb", "b1") != domain.DeployReady {
		t.Fatal("deploys of other sites must not be touched")
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "u@x.io", domain.UserActive)
	v := mkUser(t, db, "v@x.io", domain.UserActive)
	entries := []domain.AuditEntry{
		{ActorID: &u, Action: "site.create", Target: "s1", Meta: map[string]any{"title": "A"}, IP: "1.1.1.1", CreatedAt: t0},
		{ActorID: &u, Action: "deploy.publish", Target: "s1", CreatedAt: t0.Add(time.Minute)},
		{ActorID: &v, Action: "site.create", Target: "s2", CreatedAt: t0.Add(2 * time.Minute)},
		{Action: "auth.login_failed", Meta: map[string]any{"email": "x@y"}, CreatedAt: t0.Add(3 * time.Minute)},
	}
	for _, e := range entries {
		must(t, db.InsertAudit(ctx, e))
	}

	all, total, err := db.ListAudit(ctx, AuditFilter{})
	must(t, err)
	if total != 4 || all[0].Action != "auth.login_failed" || all[0].ActorID != nil || all[0].ActorEmail != "" {
		t.Fatalf("all %d %+v", total, all)
	}
	last := all[3]
	if last.ActorEmail != "u@x.io" || last.Meta["title"] != "A" || last.IP != "1.1.1.1" || !last.CreatedAt.Equal(t0) {
		t.Fatalf("oldest %+v", last)
	}
	if all[2].Meta == nil || len(all[2].Meta) != 0 {
		t.Fatalf("nil meta must round-trip as empty map: %+v", all[2].Meta)
	}

	cases := []struct {
		name string
		f    AuditFilter
		want []string
	}{
		{"actor", AuditFilter{ActorID: &u}, []string{"deploy.publish", "site.create"}},
		{"action", AuditFilter{Action: "site.create"}, []string{"site.create", "site.create"}},
		{"actor+action", AuditFilter{ActorID: &v, Action: "site.create"}, []string{"site.create"}},
		{"from inclusive", AuditFilter{From: ptr(t0.Add(2 * time.Minute))}, []string{"auth.login_failed", "site.create"}},
		{"to exclusive", AuditFilter{To: ptr(t0.Add(time.Minute))}, []string{"site.create"}},
		{"page", AuditFilter{Page: Page{Limit: 1, Offset: 1}}, []string{"site.create"}},
	}
	for _, c := range cases {
		got, total, err := db.ListAudit(ctx, c.f)
		must(t, err)
		actions := []string{}
		for _, r := range got {
			actions = append(actions, r.Action)
		}
		if !slices.Equal(actions, c.want) {
			t.Errorf("%s: %v, want %v", c.name, actions, c.want)
		}
		if c.name != "page" && total != len(c.want) {
			t.Errorf("%s: total %d", c.name, total)
		}
	}

	actions, err := db.ListAuditActions(ctx)
	must(t, err)
	if !slices.Equal(actions, []string{"auth.login_failed", "deploy.publish", "site.create"}) {
		t.Fatalf("actions %v", actions)
	}

	err = db.InsertAudit(ctx, domain.AuditEntry{Action: "bad", Meta: map[string]any{"f": func() {}}, CreatedAt: t0})
	if err == nil {
		t.Fatal("unencodable meta must fail")
	}
}

func TestUniqueViolationDetection(t *testing.T) {
	if isUniqueViolation(errors.New("UNIQUE constraint failed")) {
		t.Fatal("text must not be enough")
	}
	db := newDB(t)
	_, err := db.Writer().ExecContext(context.Background(), "INSERT INTO settings VALUES ('k','v'), ('k','w')")
	if !isUniqueViolation(err) {
		t.Fatalf("primary key violation not detected: %v", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	owner := mkUser(t, db, "owner@x.io", domain.UserActive)
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Go(func() {
			errs <- concurrentWork(ctx, db, owner, i)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.CountUsers(ctx)
	must(t, err)
	sites, total, err := db.ListSites(ctx, SiteFilter{})
	must(t, err)
	if n != workers+1 || total != workers || len(sites) != workers {
		t.Fatalf("users %d sites %d", n, total)
	}
}

func concurrentWork(ctx context.Context, db *DB, owner int64, i int) error {
	id, err := db.CreateUser(ctx, domain.User{Email: fmt.Sprintf("w%d@x.io", i), Role: domain.RoleUser,
		Status: domain.UserInvited, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		return err
	}
	if _, err := db.CreateInvite(ctx, newInvite(id, fmt.Sprintf("h%d", i), t0, t0.Add(time.Hour))); err != nil {
		return err
	}
	siteID := fmt.Sprintf("site%06d", i)
	if err := createSite(db, siteID, owner, fmt.Sprintf("slug-%d", i), t0); err != nil {
		return err
	}
	err = db.InsertDeployAndActivate(ctx, domain.Deploy{ID: "d" + siteID, SiteID: siteID, CreatedBy: owner,
		Source: domain.SourceCLI, Status: domain.DeployReady, CreatedAt: t0}, t0)
	if err != nil {
		return err
	}
	if _, _, err := db.ListSites(ctx, SiteFilter{}); err != nil {
		return err
	}
	return db.InsertAudit(ctx, domain.AuditEntry{ActorID: &id, Action: "site.create", Target: siteID, CreatedAt: t0})
}
