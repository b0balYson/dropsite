package service

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

func (e *env) current(a Actor, id string) string {
	e.t.Helper()
	v, err := e.svc.GetSite(context.Background(), a, id)
	if err != nil {
		e.t.Fatal(err)
	}
	if v.Current == nil {
		return ""
	}
	return v.Current.ID
}

func (e *env) redeploy(a Actor, id, body string) string {
	e.t.Helper()
	e.clock.Advance(time.Second)
	res, err := e.svc.Deploy(context.Background(), a, id, zipOf(e.t, map[string]string{"index.html": body}), domain.SourceUI)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Deploy.ID
}

func TestScheduleReleaseRunsWhenDue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, first := e.published(u, shop)
	second := e.redeploy(u, id, "<p>v2</p>")
	at := e.clock.Now().Add(time.Hour)

	_, err := e.svc.ScheduleRelease(ctx, u, id, second, at)
	wantCode(t, err, domain.CodeScheduleInvalid) // already served
	_, err = e.svc.ScheduleRelease(ctx, u, id, first, e.clock.Now().Add(-time.Minute))
	wantCode(t, err, domain.CodeScheduleInvalid)
	_, err = e.svc.ScheduleRelease(ctx, u, id, first, e.clock.Now().Add(MaxScheduleAhead+time.Hour))
	wantCode(t, err, domain.CodeScheduleInvalid)
	_, err = e.svc.ScheduleRelease(ctx, e.user("x@pp.test"), id, first, at)
	wantNotFound(t, err)

	sv, err := e.svc.ScheduleRelease(ctx, u, id, first, at)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Status != domain.SchedulePending || sv.SiteURL == "" || sv.SiteTitle != "Shop" {
		t.Fatalf("view %+v", sv)
	}
	_, err = e.svc.ScheduleRelease(ctx, u, id, first, at.Add(time.Hour))
	wantCode(t, err, domain.CodeScheduleInvalid) // one pending publish per release

	if err := e.svc.RunDueSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	if e.current(u, id) != second {
		t.Fatal("published too early")
	}
	e.clock.Advance(time.Hour)
	if err := e.svc.RunDueSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	if e.current(u, id) != first {
		t.Fatal("not published when due")
	}
	list, err := e.svc.ListSchedules(ctx, u, "")
	if err != nil || len(list) != 1 || list[0].Status != domain.ScheduleDone || list[0].FinishedAt == nil {
		t.Fatalf("list %+v %v", list, err)
	}
	if e.auditCount("schedule.publish") != 1 || e.auditCount("schedule.create") != 1 {
		t.Fatal("audit")
	}
	_, err = e.svc.CancelSchedule(ctx, u, list[0].ID)
	wantCode(t, err, domain.CodeScheduleNotPending)
}

func TestScheduleUploadPreparesRelease(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, first := e.published(u, shop)
	up, err := e.svc.BeginUpload(ctx, u, id, "", UploadReplace, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.UploadFiles(ctx, u, up.ID, []InlineFile{{Path: "index.html", Content: "<p>sale</p>"}}, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ScheduleUpload(ctx, u, up.ID, e.clock.Now())
	wantCode(t, err, domain.CodeScheduleInvalid)

	at := e.clock.Now().Add(24 * time.Hour)
	sv, err := e.svc.ScheduleUpload(ctx, u, up.ID, at) // the session is still open after the bad time
	if err != nil {
		t.Fatal(err)
	}
	if e.current(u, id) != first {
		t.Fatal("prepared release went live")
	}
	if body, _ := e.served(host, "index.html"); body != shop["index.html"] {
		t.Fatalf("visitors see %q", body)
	}
	d, err := e.repo.GetDeploy(ctx, id, sv.DeployID)
	if err != nil || d.Status != domain.DeployReady {
		t.Fatalf("prepared %+v %v", d, err)
	}
	if _, _, err := e.svc.UploadFiles(ctx, u, up.ID, nil, nil); err == nil {
		t.Fatal("session still open")
	}
	e.clock.Advance(25 * time.Hour)
	if err := e.svc.RunDueSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	if body, _ := e.served(host, "index.html"); body != "<p>sale</p>" {
		t.Fatalf("after publish visitors see %q", body)
	}
}

func TestScheduledReleaseSurvivesPruneAndRollback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, first := e.published(u, shop)
	second := e.redeploy(u, id, "<p>2</p>")
	if _, err := e.svc.ScheduleRelease(ctx, u, id, first, e.clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		e.redeploy(u, id, "<p>more</p>"+string(rune('a'+i)))
	}
	d, err := e.repo.GetDeploy(ctx, id, first)
	if err != nil || d.Status != domain.DeployReady {
		t.Fatalf("scheduled release pruned: %+v %v", d, err)
	}
	if d, _ := e.repo.GetDeploy(ctx, id, second); d.Status != domain.DeployPruned {
		t.Fatalf("unscheduled old release kept: %s", d.Status)
	}
	// Rolling back to "the previous one" never picks the scheduled release.
	for {
		res, err := e.svc.Rollback(ctx, u, id, "")
		if err != nil {
			wantCode(t, err, domain.CodeNoPrevious)
			break
		}
		if res.Deploy.ID == first {
			t.Fatal("rollback picked the scheduled release")
		}
	}
}

func TestScheduleCancelAndDeletedSite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, first := e.published(u, shop)
	e.redeploy(u, id, "<p>2</p>")
	sv, err := e.svc.ScheduleRelease(ctx, u, id, first, e.clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CancelSchedule(ctx, e.user("x@pp.test"), sv.ID)
	wantNotFound(t, err)
	got, err := e.svc.CancelSchedule(ctx, u, sv.ID)
	if err != nil || got.Status != domain.ScheduleCancelled {
		t.Fatalf("cancel %+v %v", got, err)
	}
	sv, err = e.svc.ScheduleRelease(ctx, u, id, first, e.clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteSite(ctx, u, id); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(2 * time.Hour)
	if err := e.svc.RunDueSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	sp, err := e.repo.GetSchedule(ctx, sv.ID)
	if err != nil || sp.Status != domain.ScheduleCancelled {
		t.Fatalf("deleted site: %+v %v", sp, err)
	}
}

func TestScheduleLimitAndParse(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Limits.KeepReleases = 30 })
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	at := e.clock.Now().Add(time.Hour)
	for i := 0; i <= MaxPendingSchedules; i++ {
		e.redeploy(u, id, "<p>x</p>"+string(rune('a'+i)))
	}
	ds, _, err := e.svc.ListDeploys(ctx, u, id)
	if err != nil {
		t.Fatal(err)
	}
	n, limited := 0, false
	for _, d := range ds {
		if d.Status != domain.DeployReady || d.ID == e.current(u, id) {
			continue
		}
		_, err := e.svc.ScheduleRelease(ctx, u, id, d.ID, at)
		if err != nil {
			wantCode(t, err, domain.CodeScheduleLimit)
			limited = true
			break
		}
		n++
	}
	if n != MaxPendingSchedules || !limited {
		t.Fatalf("scheduled %d", n)
	}
	for _, bad := range []string{"", "2026-12-01T09:00:00", "tomorrow"} {
		_, err := ParsePublishAt(bad)
		wantCode(t, err, domain.CodeScheduleInvalid)
	}
	got, err := ParsePublishAt("2026-12-01T09:00:00+03:00")
	if err != nil || !got.Equal(time.Date(2026, 12, 1, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v %v", got, err)
	}
}
