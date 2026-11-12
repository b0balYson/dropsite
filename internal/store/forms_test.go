package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func mkChannel(t *testing.T, db *DB, site string, kind domain.ChannelKind, target string, status domain.ChannelStatus) int64 {
	t.Helper()
	id, err := db.CreateFormChannel(context.Background(), domain.FormChannel{
		SiteID: site, Kind: kind, Target: target, Status: status, Enabled: true, CreatedAt: t0, UpdatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFormSiteByHostAndSettings(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "promo", t0)
	for _, h := range []string{"site000001." + base, "promo." + base} {
		s, err := db.GetSiteByHost(ctx, h)
		if err != nil || s.ID != "site000001" {
			t.Fatalf("%s: %v %v", h, s.ID, err)
		}
	}
	if _, err := db.GetSiteByHost(ctx, "nope."+base); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown host: %v", err)
	}
	st, err := db.GetFormSiteSettings(ctx, "site000001")
	if err != nil || st.RequireAltcha {
		t.Fatalf("default settings %+v %v", st, err)
	}
	if err := db.SetFormSiteSettings(ctx, domain.FormSiteSettings{SiteID: "site000001", RequireAltcha: true}); err != nil {
		t.Fatal(err)
	}
	if st, _ := db.GetFormSiteSettings(ctx, "site000001"); !st.RequireAltcha {
		t.Fatal("setting not stored")
	}
	if _, err := db.SoftDeleteSite(ctx, "site000001", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSiteByHost(ctx, "promo."+base); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted site still resolves: %v", err)
	}
}

func TestFormChannelsCRUD(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)
	id := mkChannel(t, db, "site000001", domain.ChannelTelegram, "", domain.ChannelPending)
	exp := t0.Add(time.Hour)
	ch, err := db.GetFormChannel(ctx, "site000001", id)
	if err != nil {
		t.Fatal(err)
	}
	ch.VerifyHash, ch.VerifyExpiresAt = []byte{1, 2}, &exp
	if err := db.UpdateFormChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	links, err := db.ListPendingTelegramLinks(ctx, t0)
	if err != nil || len(links) != 1 || links[0].ID != id {
		t.Fatalf("pending links %v %v", links, err)
	}
	if links, _ := db.ListPendingTelegramLinks(ctx, exp.Add(time.Second)); len(links) != 0 {
		t.Fatal("expired link listed")
	}
	if _, err := db.GetFormChannel(ctx, "site000002", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("channel visible through another site")
	}
	if err := db.DeleteFormChannel(ctx, "site000002", id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted through another site")
	}
	if n, _ := db.CountFormChannels(ctx, "site000001"); n != 1 {
		t.Fatalf("count %d", n)
	}
	if err := db.DeleteFormChannel(ctx, "site000001", id); err != nil {
		t.Fatal(err)
	}
}

func TestFormSubmissionsQueueAndPurge(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	active := mkChannel(t, db, "site000001", domain.ChannelWebhook, "https://h.test/", domain.ChannelActive)
	mkChannel(t, db, "site000001", domain.ChannelEmail, "o@x.test", domain.ChannelPending)
	disabled := mkChannel(t, db, "site000001", domain.ChannelWebhook, "https://d.test/", domain.ChannelActive)
	ch, _ := db.GetFormChannel(ctx, "site000001", disabled)
	ch.Enabled = false
	if err := db.UpdateFormChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	fields := []domain.FormField{{Name: "name", Value: "Ann"}, {Name: "a", Value: "1"}}
	for i, form := range []string{"lead", "call", "lead"} {
		_, err := db.InsertFormSubmission(ctx, domain.FormSubmission{SiteID: "site000001", Host: "h", Form: form,
			Fields: fields, CreatedAt: t0.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
	}
	subs, total, err := db.ListFormSubmissions(ctx, FormSubmissionFilter{SiteID: "site000001", Form: "lead"})
	if err != nil || total != 2 || len(subs) != 2 || subs[0].CreatedAt.Before(subs[1].CreatedAt) {
		t.Fatalf("list %v %d %v", subs, total, err)
	}
	if subs[0].Fields[0] != fields[0] || len(subs[0].Deliveries) != 1 || subs[0].Deliveries[0].ChannelID != active {
		t.Fatalf("submission %+v", subs[0])
	}
	names, _ := db.ListFormNames(ctx, "site000001")
	if len(names) != 2 || names[0] != "call" {
		t.Fatalf("names %v", names)
	}

	now := t0.Add(time.Hour)
	jobs, err := db.ClaimDueDeliveries(ctx, now, now.Add(2*time.Minute), 2)
	if err != nil || len(jobs) != 2 || jobs[0].Channel.Target != "https://h.test/" || jobs[0].Submission.Fields[0].Value != "Ann" {
		t.Fatalf("claim %+v %v", jobs, err)
	}
	again, _ := db.ClaimDueDeliveries(ctx, now, now.Add(2*time.Minute), 10)
	if len(again) != 1 {
		t.Fatalf("leased jobs were claimed again: %d", len(again))
	}
	d := jobs[0].Delivery
	sent := now
	d.Status, d.Attempts, d.SentAt = domain.DeliverySent, 1, &sent
	if err := db.FinishDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}

	ok, err := db.UseAltchaSolution(ctx, "sig", now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatal("first use")
	}
	if ok, _ := db.UseAltchaSolution(ctx, "sig", now.Add(time.Minute)); ok {
		t.Fatal("replay accepted")
	}

	n, err := db.PurgeForms(ctx, t0.Add(90*time.Second), now.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("purge %d %v", n, err)
	}
	if ok, _ := db.UseAltchaSolution(ctx, "sig", now.Add(2*time.Hour)); !ok {
		t.Fatal("expired altcha record not purged")
	}
	if _, err := db.SoftDeleteSite(ctx, "site000001", now); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.PurgeForms(ctx, t0, now); n != 1 {
		t.Fatalf("deleted site purge %d", n)
	}
	if c, _ := db.CountFormChannels(ctx, "site000001"); c != 0 {
		t.Fatal("channels of deleted site kept")
	}
}
