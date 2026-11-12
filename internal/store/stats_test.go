package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/stats"
)

var lim = StatsLimits{Pages: 3, Referrers: 2}

func batchOf(views map[stats.Key]int64, pages map[stats.Key]map[string]int64, visitors map[stats.Key][]string) *stats.Batch {
	b := stats.NewBatch()
	for k, v := range views {
		b.Views[k] = v
	}
	for k, p := range pages {
		b.Pages[k] = p
	}
	for k, hs := range visitors {
		set := map[string]struct{}{}
		for _, h := range hs {
			set[h] = struct{}{}
		}
		b.Visitors[k] = set
	}
	return b
}

func TestStatsBatchAndCursor(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	c, err := db.StatsCursor(ctx)
	if err != nil || c != (stats.Cursor{}) {
		t.Fatalf("initial cursor %+v, %v", c, err)
	}
	k := stats.Key{SiteID: "s1", Day: "2026-11-12"}
	b := batchOf(map[stats.Key]int64{k: 3}, map[stats.Key]map[string]int64{k: {"/": 2, "/a": 1}},
		map[stats.Key][]string{k: {"h1", "h2"}})
	b.Referrers[k] = map[string]int64{"": 2, "t.me": 1}
	must(t, db.SaveStatsBatch(ctx, b, stats.Cursor{Inode: 1 << 62, Offset: 100}, lim, t0))
	// The same visitor again plus a new one: only the new one is added.
	b2 := batchOf(map[stats.Key]int64{k: 2}, map[stats.Key]map[string]int64{k: {"/": 2}},
		map[stats.Key][]string{k: {"h2", "h3"}})
	must(t, db.SaveStatsBatch(ctx, b2, stats.Cursor{Inode: 1 << 62, Offset: 200}, lim, t0))

	c, err = db.StatsCursor(ctx)
	if err != nil || c != (stats.Cursor{Inode: 1 << 62, Offset: 200}) {
		t.Fatalf("cursor %+v, %v", c, err)
	}
	days, err := db.StatsDaily(ctx, "s1", "2026-11-01", "2026-11-30")
	if err != nil || len(days) != 1 || days[0] != (domain.DayStat{Day: "2026-11-12", Views: 5, Visitors: 3}) {
		t.Fatalf("days %+v, %v", days, err)
	}
	pages, err := db.StatsTopPages(ctx, "s1", "2026-11-12", "2026-11-12", 10)
	if err != nil || len(pages) != 2 || pages[0] != (domain.NamedCount{Name: "/", Views: 4}) {
		t.Fatalf("pages %+v, %v", pages, err)
	}
	refs, err := db.StatsTopReferrers(ctx, "s1", "2026-11-12", "2026-11-12", 1)
	if err != nil || len(refs) != 1 || refs[0] != (domain.NamedCount{Name: "", Views: 2}) {
		t.Fatalf("refs %+v, %v", refs, err)
	}
}

func TestStatsRowLimitFoldsIntoOther(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	k := stats.Key{SiteID: "s1", Day: "2026-11-12"}
	pages := map[string]int64{}
	for i := range 10 {
		pages[fmt.Sprintf("/p%d", i)] = 1
	}
	must(t, db.SaveStatsBatch(ctx, batchOf(map[stats.Key]int64{k: 10}, map[stats.Key]map[string]int64{k: pages}, nil),
		stats.Cursor{}, lim, t0))
	// A second batch with known and unknown paths.
	must(t, db.SaveStatsBatch(ctx, batchOf(nil, map[stats.Key]map[string]int64{k: {"/new": 5}}, nil),
		stats.Cursor{}, lim, t0))
	top, err := db.StatsTopPages(ctx, "s1", k.Day, k.Day, 100)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	var other int64
	for _, p := range top {
		total += p.Views
		if p.Name == stats.Other {
			other = p.Views
		}
	}
	if len(top) != lim.Pages+1 || total != 15 || other != 12 {
		t.Fatalf("top %+v (total %d, other %d)", top, total, other)
	}
}

func TestStatsSaltAndRetention(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	s, err := db.StatsSalt(ctx, "2026-11-12", false)
	if err != nil || s != nil {
		t.Fatalf("salt without create: %x, %v", s, err)
	}
	s1, err := db.StatsSalt(ctx, "2026-11-12", true)
	if err != nil || len(s1) != stats.SaltBytes {
		t.Fatalf("salt %x, %v", s1, err)
	}
	s2, err := db.StatsSalt(ctx, "2026-11-12", true)
	if err != nil || !bytes.Equal(s1, s2) {
		t.Fatal("salt must be stable within a day")
	}
	old := stats.Key{SiteID: "s1", Day: "2026-11-11"}
	cur := stats.Key{SiteID: "s1", Day: "2026-11-12"}
	_, _ = db.StatsSalt(ctx, old.Day, true)
	must(t, db.SaveStatsBatch(ctx, batchOf(map[stats.Key]int64{old: 1, cur: 1}, nil,
		map[stats.Key][]string{old: {"a"}, cur: {"b"}}), stats.Cursor{}, lim, t0))
	must(t, db.DeleteVisitorData(ctx, "2026-11-12"))
	if s, _ := db.StatsSalt(ctx, old.Day, false); s != nil {
		t.Error("old salt must be deleted")
	}
	if s, _ := db.StatsSalt(ctx, cur.Day, false); s == nil {
		t.Error("current salt must stay")
	}
	var n int
	must(t, db.Reader().QueryRowContext(ctx, "SELECT count(*) FROM stats_visitors").Scan(&n))
	if n != 1 {
		t.Errorf("visitor rows = %d, want 1", n)
	}
	must(t, db.DeleteStatsBefore(ctx, "2026-11-12"))
	days, _ := db.StatsDaily(ctx, "s1", "2000-01-01", "2100-01-01")
	if len(days) != 1 || days[0].Day != cur.Day || days[0].Visitors != 1 {
		t.Fatalf("days after retention %+v", days)
	}
}

func TestSitesViews(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	b := batchOf(map[stats.Key]int64{
		{SiteID: "a", Day: "2026-11-01"}: 1, {SiteID: "a", Day: "2026-11-10"}: 2,
		{SiteID: "b", Day: "2026-11-10"}: 4, {SiteID: "c", Day: "2026-11-10"}: 8,
	}, nil, nil)
	must(t, db.SaveStatsBatch(ctx, b, stats.Cursor{}, lim, t0))
	got, err := db.SitesViews(ctx, []string{"a", "b", "z"}, "2026-11-05", "2026-11-12")
	if err != nil || len(got) != 2 || got["a"] != 2 || got["b"] != 4 {
		t.Fatalf("views %v, %v", got, err)
	}
	if got, err := db.SitesViews(ctx, nil, "a", "b"); err != nil || len(got) != 0 {
		t.Fatalf("empty ids: %v, %v", got, err)
	}
}

func TestSiteCountersAndPurge(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	owner := mkUser(t, db, "o@example.test", domain.UserActive)
	mkSite(t, db, "site1", owner, "", t0)
	c, err := db.GetSiteCounters(ctx, "site1")
	if err != nil || !c.Empty() {
		t.Fatalf("initial counters %+v, %v", c, err)
	}
	want := domain.Counters{MetrikaID: "12345678", GA4ID: "G-ABCD1234"}
	must(t, db.SetSiteCounters(ctx, "site1", want, t0))
	if c, _ := db.GetSiteCounters(ctx, "site1"); c != want {
		t.Fatalf("counters %+v", c)
	}
	if err := db.SetSiteCounters(ctx, "missing", want, t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing site: %v", err)
	}
	must(t, db.SetSiteCounters(ctx, "site1", domain.Counters{}, t0))
	if c, _ := db.GetSiteCounters(ctx, "site1"); !c.Empty() {
		t.Fatalf("cleared counters %+v", c)
	}
	must(t, db.SetSiteCounters(ctx, "site1", want, t0))
	k := stats.Key{SiteID: "site1", Day: "2026-11-12"}
	must(t, db.SaveStatsBatch(ctx, batchOf(map[stats.Key]int64{k: 1}, map[stats.Key]map[string]int64{k: {"/": 1}},
		map[stats.Key][]string{k: {"x"}}), stats.Cursor{}, lim, t0))
	if _, err := db.SoftDeleteSite(ctx, "site1", t0); err != nil {
		t.Fatal(err)
	}
	must(t, db.MarkSitePurged(ctx, "site1", t0))
	for _, table := range []string{"stats_daily", "stats_pages", "stats_visitors", "site_counters"} {
		var n int
		must(t, db.Reader().QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Errorf("%s keeps %d rows after purge", table, n)
		}
	}
}
