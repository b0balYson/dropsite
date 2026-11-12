package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSheetLinks(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkCollection(t, db, "site000001", "prices", domain.CollectionPublic)
	mkCollection(t, db, "site000001", "menu", domain.CollectionPublic)

	if _, err := db.GetSheetLink(ctx, "site000001", "prices"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing link: %v", err)
	}
	next := t0.Add(5 * time.Minute)
	l := domain.SheetLink{SiteID: "site000001", Collection: "prices", SpreadsheetID: "doc", SheetGID: ptr(int64(7)),
		SheetTitle: "Prices", IntervalMinutes: 5, NextSyncAt: &next, CreatedAt: t0, UpdatedAt: t0}
	created, err := db.PutSheetLink(ctx, l)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if _, err := db.PutSheetLink(ctx, domain.SheetLink{SiteID: "site000001", Collection: "nope", CreatedAt: t0, UpdatedAt: t0}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown collection: %v", err)
	}
	must(t, db.RecordSheetSync(ctx, "site000001", "prices", domain.SheetSyncResult{At: t0, Status: domain.SheetSyncFailed,
		Trigger: domain.SheetTriggerManual, Rows: 3, Message: "bad rows",
		Errors: []domain.SheetRowError{{Row: 4, Field: "price", Message: "not a number"}}}))
	got, err := db.GetSheetLink(ctx, "site000001", "prices")
	if err != nil || got.SheetGID == nil || *got.SheetGID != 7 || got.LastStatus != domain.SheetSyncFailed ||
		len(got.LastErrors) != 1 || got.LastErrors[0].Row != 4 || got.SheetTitle != "Prices" || got.LastRows != 3 {
		t.Fatalf("get: %+v %v", got, err)
	}
	l.IntervalMinutes, l.SheetGID = 0, nil
	created, err = db.PutSheetLink(ctx, l)
	if err != nil || created {
		t.Fatalf("update: %v %v", created, err)
	}
	got, _ = db.GetSheetLink(ctx, "site000001", "prices")
	if got.SheetGID != nil || got.IntervalMinutes != 0 || got.LastStatus != domain.SheetSyncFailed {
		t.Fatalf("after update: %+v", got)
	}

	must(t, db.DeleteSheetLink(ctx, "site000001", "prices"))
	if err := db.DeleteSheetLink(ctx, "site000001", "prices"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	// Deleting the collection removes its link.
	_, err = db.PutSheetLink(ctx, domain.SheetLink{SiteID: "site000001", Collection: "menu", SpreadsheetID: "doc",
		CreatedAt: t0, UpdatedAt: t0})
	must(t, err)
	must(t, db.DeleteCollection(ctx, "site000001", "menu"))
	if _, err := db.GetSheetLink(ctx, "site000001", "menu"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("link survived its collection: %v", err)
	}
}

func TestClaimDueSheetLinks(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)
	for _, s := range []string{"site000001", "site000002"} {
		mkCollection(t, db, s, "a", domain.CollectionPublic)
		mkCollection(t, db, s, "b", domain.CollectionPublic)
	}
	due := t0
	later := t0.Add(time.Hour)
	put := func(site, coll string, interval int, next *time.Time) {
		_, err := db.PutSheetLink(ctx, domain.SheetLink{SiteID: site, Collection: coll, SpreadsheetID: "doc",
			IntervalMinutes: interval, NextSyncAt: next, CreatedAt: t0, UpdatedAt: t0})
		must(t, err)
	}
	put("site000001", "a", 15, &due)   // due
	put("site000001", "b", 0, &due)    // manual only
	put("site000002", "a", 15, &later) // not yet
	put("site000002", "b", 10, &due)   // due, but the site is deleted below
	_, err := db.w.ExecContext(ctx, "UPDATE sites SET deleted_at = ? WHERE id = 'site000002'", toMS(t0))
	must(t, err)

	now := t0.Add(time.Minute)
	got, err := db.ClaimDueSheetLinks(ctx, now, 10)
	if err != nil || len(got) != 1 || got[0].SiteID != "site000001" || got[0].Collection != "a" {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if !got[0].NextSyncAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("next %v", got[0].NextSyncAt)
	}
	again, err := db.ClaimDueSheetLinks(ctx, now, 10)
	if err != nil || len(again) != 0 {
		t.Fatalf("claimed twice: %+v %v", again, err)
	}
}

// TestSheetsChannelMigration checks the rebuilt channel tables: the new
// kind is accepted, deliveries still cascade from channels, and the rebuild
// keeps existing rows when it runs on a populated database.
func TestSheetsChannelMigration(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	hook := mkChannel(t, db, "site000001", domain.ChannelWebhook, "https://h.example/x", domain.ChannelActive)
	sheet := mkChannel(t, db, "site000001", domain.ChannelSheets, "https://docs.google.com/spreadsheets/d/x/edit", domain.ChannelActive)
	sub, err := db.InsertFormSubmission(ctx, domain.FormSubmission{SiteID: "site000001", Host: "h", Form: "lead", CreatedAt: t0})
	must(t, err)

	body, err := migrationFS.ReadFile("migrations/0015_google_sheets.sql")
	must(t, err)
	rebuild, _, ok := strings.Cut(string(body), "-- A collection linked")
	if !ok {
		t.Fatal("migration layout changed")
	}
	tx, err := db.w.BeginTx(ctx, nil)
	must(t, err)
	if _, err := tx.ExecContext(ctx, rebuild); err != nil {
		_ = tx.Rollback()
		t.Fatalf("rebuild on populated tables: %v", err)
	}
	must(t, tx.Commit())

	var deliveries int
	must(t, db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM form_deliveries WHERE submission_id = ?", sub).Scan(&deliveries))
	if deliveries != 2 {
		t.Fatalf("deliveries after rebuild: %d", deliveries)
	}
	var parent string
	must(t, db.r.QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_list('form_deliveries') WHERE "from" = 'channel_id'`).Scan(&parent))
	if parent != "form_channels" {
		t.Fatalf("form_deliveries.channel_id references %q", parent)
	}
	must(t, db.DeleteFormChannel(ctx, "site000001", sheet))
	must(t, db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM form_deliveries WHERE submission_id = ?", sub).Scan(&deliveries))
	if deliveries != 1 {
		t.Fatalf("delivery of a deleted channel kept: %d", deliveries)
	}
	if _, err := db.GetFormChannel(ctx, "site000001", hook); err != nil {
		t.Fatalf("webhook channel lost: %v", err)
	}
}
