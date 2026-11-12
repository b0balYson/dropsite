package store

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSchedules(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner := mkUser(t, db, "o@example.test", domain.UserActive)
	other := mkUser(t, db, "x@example.test", domain.UserActive)
	mkSite(t, db, "s1", owner, "", t0)
	mkSite(t, db, "s2", other, "", t0)
	mkDeploy(t, db, "s1", "d1", owner, 1, t0)
	prepared := domain.Deploy{ID: "d2", SiteID: "s1", CreatedBy: owner, Source: domain.SourceMCP, FileCount: 1,
		Status: domain.DeployReady, CreatedAt: t0.Add(time.Minute)}
	if err := db.InsertDeploy(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	site, err := db.GetSite(ctx, "s1")
	if err != nil || *site.CurrentDeployID != "d1" {
		t.Fatalf("prepared release went live: %v", err)
	}

	at := t0.Add(time.Hour)
	sp, err := db.CreateSchedule(ctx, domain.ScheduledPublish{SiteID: "s1", DeployID: "d2", CreatedBy: &owner,
		PublishAt: at, CreatedAt: t0}, 2)
	if err != nil || sp.ID == 0 || sp.Status != domain.SchedulePending {
		t.Fatalf("create %+v %v", sp, err)
	}
	_, err = db.CreateSchedule(ctx, domain.ScheduledPublish{SiteID: "s1", DeployID: "d2", PublishAt: at, CreatedAt: t0}, 2)
	wantCode(t, err, domain.CodeScheduleInvalid)
	if _, err := db.CreateSchedule(ctx, domain.ScheduledPublish{SiteID: "s1", DeployID: "d1", PublishAt: at, CreatedAt: t0}, 2); err != nil {
		t.Fatal(err)
	}
	mkDeploy(t, db, "s1", "d3", owner, 1, t0.Add(2*time.Minute))
	_, err = db.CreateSchedule(ctx, domain.ScheduledPublish{SiteID: "s1", DeployID: "d3", PublishAt: at, CreatedAt: t0}, 2)
	wantCode(t, err, domain.CodeScheduleLimit)

	pending, err := db.PendingScheduledDeploys(ctx, "s1")
	if err != nil || !pending["d1"] || !pending["d2"] || len(pending) != 2 {
		t.Fatalf("pending %v %v", pending, err)
	}
	if due, _ := db.ListDueSchedules(ctx, at.Add(-time.Second), 10); len(due) != 0 {
		t.Fatalf("due early: %v", due)
	}
	due, err := db.ListDueSchedules(ctx, at, 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("due %v %v", due, err)
	}
	if err := db.FinishSchedule(ctx, sp.ID, domain.ScheduleDone, "", at); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, db.FinishSchedule(ctx, sp.ID, domain.ScheduleCancelled, "", at))
	got, err := db.GetSchedule(ctx, sp.ID)
	if err != nil || got.Status != domain.ScheduleDone || got.FinishedAt == nil || *got.CreatedBy != owner {
		t.Fatalf("get %+v %v", got, err)
	}
	list, err := db.ListSchedules(ctx, owner, "", at.Add(-time.Hour), 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("owner list %v %v", list, err)
	}
	if list, _ := db.ListSchedules(ctx, owner, "", at.Add(time.Hour), 10); len(list) != 1 {
		t.Fatalf("old finished rows listed: %v", list)
	}
	if list, _ := db.ListSchedules(ctx, other, "", t0, 10); len(list) != 0 {
		t.Fatalf("other owner sees %v", list)
	}
	if err := db.InsertDeploy(ctx, domain.Deploy{ID: "dx", SiteID: "nope", CreatedBy: owner, Source: domain.SourceAPI,
		Status: domain.DeployReady, CreatedAt: t0}); err == nil {
		t.Fatal("deploy for a missing site")
	}
}
