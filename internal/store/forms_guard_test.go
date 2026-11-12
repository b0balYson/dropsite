package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func submitN(t *testing.T, db *DB, site string, n int, at time.Time) {
	t.Helper()
	for i := range n {
		_, err := db.InsertFormSubmission(context.Background(), domain.FormSubmission{SiteID: site, Host: "h", Form: "lead",
			Fields: []domain.FormField{{Name: "n", Value: fmt.Sprint(i)}}, CreatedAt: at.Add(time.Duration(i) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func claim(t *testing.T, db *DB, c DeliveryClaim) []DeliveryJob {
	t.Helper()
	jobs, err := db.ClaimDueDeliveries(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func TestClaimInterleavesSitesAndHonoursAccept(t *testing.T) {
	db := newDB(t)
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)
	// The noisy site has three channels and an older, larger backlog.
	for i := range 3 {
		mkChannel(t, db, "site000001", domain.ChannelWebhook, fmt.Sprintf("https://n%d.test/", i), domain.ChannelActive)
	}
	quiet := mkChannel(t, db, "site000002", domain.ChannelWebhook, "https://q.test/", domain.ChannelActive)
	submitN(t, db, "site000001", 20, t0)
	submitN(t, db, "site000002", 1, t0.Add(time.Hour))
	now := t0.Add(2 * time.Hour)

	jobs := claim(t, db, DeliveryClaim{Now: now, LeaseUntil: now.Add(time.Minute), Limit: 2})
	if len(jobs) != 2 {
		t.Fatalf("first claim %v", ids(jobs))
	}
	sites := map[string]bool{jobs[0].Channel.SiteID: true, jobs[1].Channel.SiteID: true}
	if !sites["site000002"] || (jobs[0].Channel.ID != quiet && jobs[1].Channel.ID != quiet) {
		t.Fatal("the quiet site waited behind the noisy one")
	}

	// Accept can cap a site: only one of the noisy site's channels passes.
	perSite := map[string]int{}
	jobs = claim(t, db, DeliveryClaim{Now: now, LeaseUntil: now.Add(time.Minute), Limit: 10, Accept: func(j DeliveryJob) bool {
		if perSite[j.Channel.SiteID] >= 1 {
			return false
		}
		perSite[j.Channel.SiteID]++
		return true
	}})
	if len(jobs) != 1 || jobs[0].Channel.SiteID != "site000001" {
		t.Fatalf("capped claim %v", ids(jobs))
	}
}

func ids(jobs []DeliveryJob) []int64 {
	out := make([]int64, len(jobs))
	for i, j := range jobs {
		out[i] = j.Channel.ID
	}
	return out
}

func TestClaimCancelsTurnedOffChannelsAndDeletedSites(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)
	off := mkChannel(t, db, "site000001", domain.ChannelWebhook, "https://off.test/", domain.ChannelActive)
	mkChannel(t, db, "site000002", domain.ChannelWebhook, "https://gone.test/", domain.ChannelActive)
	submitN(t, db, "site000001", 1, t0)
	submitN(t, db, "site000002", 1, t0)
	ch, _ := db.GetFormChannel(ctx, "site000001", off)
	ch.Enabled = false
	if err := db.UpdateFormChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SoftDeleteSite(ctx, "site000002", t0); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(time.Minute)
	if jobs := claim(t, db, DeliveryClaim{Now: now, LeaseUntil: now.Add(time.Minute), Limit: 10}); len(jobs) != 0 {
		t.Fatalf("claimed %v", ids(jobs))
	}
	subs, _, err := db.ListFormSubmissions(ctx, FormSubmissionFilter{SiteID: "site000001"})
	if err != nil || subs[0].Deliveries[0].Status != domain.DeliveryCancelled || subs[0].Deliveries[0].LastError != DeliveryCancelledMsg {
		t.Fatalf("delivery %+v %v", subs[0].Deliveries, err)
	}
}

func TestCircuitBreakerPausesAndResumes(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	id := mkChannel(t, db, "site000001", domain.ChannelWebhook, "https://slow.test/", domain.ChannelActive)
	submitN(t, db, "site000001", 1, t0)
	for i := 1; i <= 3; i++ {
		paused, err := db.RecordChannelResult(ctx, id, false, 3, t0)
		if err != nil || paused != (i == 3) {
			t.Fatalf("failure %d: paused=%v %v", i, paused, err)
		}
	}
	if paused, _ := db.RecordChannelResult(ctx, id, false, 3, t0); paused {
		t.Fatal("paused twice")
	}
	ch, _ := db.GetFormChannel(ctx, "site000001", id)
	if ch.PausedAt == nil || ch.ConsecutiveFailures != 4 {
		t.Fatalf("channel %+v", ch)
	}
	now := t0.Add(time.Hour)
	if jobs := claim(t, db, DeliveryClaim{Now: now, LeaseUntil: now.Add(time.Minute), Limit: 10}); len(jobs) != 0 {
		t.Fatal("paused channel claimed")
	}
	// A paused channel keeps its queue: nothing is cancelled.
	subs, _, _ := db.ListFormSubmissions(ctx, FormSubmissionFilter{SiteID: "site000001"})
	if subs[0].Deliveries[0].Status != domain.DeliveryPending {
		t.Fatalf("delivery %+v", subs[0].Deliveries[0])
	}
	if err := db.ResumeFormChannel(ctx, "site000001", id, now); err != nil {
		t.Fatal(err)
	}
	if jobs := claim(t, db, DeliveryClaim{Now: now, LeaseUntil: now.Add(time.Minute), Limit: 10}); len(jobs) != 1 {
		t.Fatal("resumed channel not claimed")
	}
	if _, err := db.RecordChannelResult(ctx, id, true, 3, now); err != nil {
		t.Fatal(err)
	}
	if ch, _ := db.GetFormChannel(ctx, "site000001", id); ch.ConsecutiveFailures != 0 || ch.PausedAt != nil {
		t.Fatalf("after success %+v", ch)
	}
}

func TestConsumeVerifyAttemptIsAtomic(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	id := mkChannel(t, db, "site000001", domain.ChannelEmail, "o@x.test", domain.ChannelPending)
	var granted atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := db.ConsumeVerifyAttempt(ctx, "site000001", id, 5, t0); err == nil && ok {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 5 {
		t.Fatalf("granted %d attempts", granted.Load())
	}
	if _, ok, _ := db.ConsumeVerifyAttempt(ctx, "site000002", id, 5, t0); ok {
		t.Fatal("attempt through another site")
	}
	ok, err := db.ActivateFormChannel(ctx, "site000001", id, t0)
	if err != nil || !ok {
		t.Fatal("activate", err)
	}
	if ok, _ := db.ActivateFormChannel(ctx, "site000001", id, t0); ok {
		t.Fatal("activated twice")
	}
}

func TestListSubmissionsBeforePages(t *testing.T) {
	db := newDB(t)
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	submitN(t, db, "site000001", 5, t0)
	page, err := db.ListSubmissionsBefore(context.Background(), "site000001", "", 1<<62, 3)
	if err != nil || len(page) != 3 || page[0].Fields[0].Value != "4" {
		t.Fatalf("page %+v %v", page, err)
	}
	rest, _ := db.ListSubmissionsBefore(context.Background(), "site000001", "lead", page[2].ID, 3)
	if len(rest) != 2 || rest[1].Fields[0].Value != "0" {
		t.Fatalf("rest %+v", rest)
	}
}
