package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

const ua = "Mozilla/5.0 (X11; Linux x86_64) Firefox/132.0"

func accessLine(at time.Time, host, uri, ip, agent, ref string) string {
	return fmt.Sprintf(`{"level":"info","ts":%d.5,"logger":"http.log.access.sites","msg":"handled request",`+
		`"request":{"client_ip":%q,"method":"GET","host":%q,"uri":%q,"headers":{"User-Agent":[%q],"Referer":[%q]}},`+
		`"status":200,"resp_headers":{"Content-Type":["text/html; charset=utf-8"]}}`+"\n",
		at.Unix(), ip, host, uri, agent, ref)
}

func appendLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func statsEnv(t *testing.T) (*env, string) {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "sites-access.log")
	e := newEnv(t, func(c *config.Config) { c.StatsLogFile = logFile })
	return e, logFile
}

func TestCollectStatsEndToEnd(t *testing.T) {
	ctx := context.Background()
	e, logFile := statsEnv(t)
	owner := e.user("owner@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.svc.CreateSite(ctx, owner, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	// No log yet: nothing to do, no error.
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	now := e.clock.Now()
	slugHost := "promo." + testBase
	defHost := v.Site.ID + "." + testBase
	appendLog(t, logFile,
		accessLine(now, slugHost, "/", "198.51.100.1", ua, "https://t.me/channel"),
		accessLine(now, slugHost+":18443", "/about?x=1", "198.51.100.1", ua, "https://"+slugHost+"/"),
		accessLine(now, defHost, "/", "198.51.100.2", ua, ""),
		accessLine(now, defHost, "/", "198.51.100.3", "Googlebot/2.1", ""),
		accessLine(now, "evil.example.org", "/", "198.51.100.4", ua, ""),
		accessLine(now.AddDate(0, 0, -1), slugHost, "/", "198.51.100.5", ua, ""),
		"not json\n",
	)
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	// A second pass (or a restart) must not count the same lines again.
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.GetSiteStats(ctx, owner, "promo", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Days) != 30 || st.To != "2026-11-12" || st.From != "2026-10-14" {
		t.Fatalf("period %s..%s, %d days", st.From, st.To, len(st.Days))
	}
	today, yesterday := st.Days[29], st.Days[28]
	if today.Views != 3 || today.Visitors != 2 {
		t.Errorf("today %+v, want 3 views and 2 visitors", today)
	}
	if yesterday.Views != 1 || yesterday.Visitors != 0 {
		t.Errorf("yesterday %+v: a closed day counts views only", yesterday)
	}
	if st.Views != 4 || st.Visitors != 2 || st.MaxViews != 3 {
		t.Errorf("totals %+v", st)
	}
	if len(st.TopPages) != 2 || st.TopPages[0] != (domain.NamedCount{Name: "/", Views: 3}) {
		t.Errorf("pages %+v", st.TopPages)
	}
	refs := map[string]int64{}
	for _, r := range st.TopReferrers {
		refs[r.Name] = r.Views
	}
	if refs["t.me"] != 1 || refs[""] != 2 || len(refs) != 2 {
		t.Errorf("referrers %+v", st.TopReferrers)
	}
	views, err := e.svc.SitesViews(ctx, e.admin, []string{v.Site.ID, other.Site.ID}, StatsAdminDays)
	if err != nil || views[v.Site.ID] != 4 || views[other.Site.ID] != 0 {
		t.Errorf("sites views %v, %v", views, err)
	}
}

func TestCollectStatsRotationAndNewDay(t *testing.T) {
	ctx := context.Background()
	e, logFile := statsEnv(t)
	owner := e.user("owner@pp.test")
	if _, err := e.svc.CreateSite(ctx, owner, "Promo", "promo"); err != nil {
		t.Fatal(err)
	}
	host := "promo." + testBase
	appendLog(t, logFile, accessLine(e.clock.Now(), host, "/", "198.51.100.1", ua, ""))
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	appendLog(t, logFile, accessLine(e.clock.Now(), host, "/", "198.51.100.1", ua, ""))
	if err := os.Rename(logFile, filepath.Join(filepath.Dir(logFile), "sites-access-2026-11-12T10-00-00.000-size.log")); err != nil {
		t.Fatal(err)
	}
	appendLog(t, logFile, accessLine(e.clock.Now(), host, "/b", "198.51.100.2", ua, ""))
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.GetSiteStats(ctx, owner, "promo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Views != 3 || st.Visitors != 2 {
		t.Fatalf("after rotation %+v", st.Days)
	}
	// Next day: yesterday's salt and visitor hashes are deleted.
	e.clock.Advance(24 * time.Hour)
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	if salt, err := e.repo.StatsSalt(ctx, "2026-11-12", false); err != nil || salt != nil {
		t.Fatalf("old salt survives: %x, %v", salt, err)
	}
	var n int
	if err := e.repo.Reader().QueryRowContext(ctx, "SELECT count(*) FROM stats_visitors").Scan(&n); err != nil || n != 0 {
		t.Fatalf("visitor hashes left: %d, %v", n, err)
	}
	st, _ = e.svc.GetSiteStats(ctx, owner, "promo", 2)
	if st.Days[0].Visitors != 2 {
		t.Fatalf("daily visitors must stay: %+v", st.Days)
	}
}

func TestSiteStatsAccess(t *testing.T) {
	ctx := context.Background()
	e, _ := statsEnv(t)
	owner := e.user("owner@pp.test")
	stranger := e.user("stranger@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetSiteStats(ctx, stranger, v.Site.ID, 30)
	wantNotFound(t, err)
	_, err = e.svc.GetSiteStats(ctx, owner, "missing", 30)
	wantNotFound(t, err)
	for _, d := range []int{0, -1, StatsMaxDays + 1} {
		_, err = e.svc.GetSiteStats(ctx, owner, v.Site.ID, d)
		wantCode(t, err, domain.CodeInvalidInput)
	}
	if _, err := e.svc.GetSiteStats(ctx, e.admin, v.Site.ID, StatsMaxDays); err != nil {
		t.Fatalf("admin: %v", err)
	}
	_, err = e.svc.SitesViews(ctx, owner, []string{v.Site.ID}, 7)
	wantNotFound(t, err)
}

func TestStatsRetentionInHousekeeping(t *testing.T) {
	ctx := context.Background()
	e, logFile := statsEnv(t)
	owner := e.user("owner@pp.test")
	if _, err := e.svc.CreateSite(ctx, owner, "Promo", "promo"); err != nil {
		t.Fatal(err)
	}
	appendLog(t, logFile, accessLine(e.clock.Now(), "promo."+testBase, "/", "198.51.100.1", ua, ""))
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(400 * 24 * time.Hour)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		t.Helper()
		var n int
		must(e.repo.Reader().QueryRowContext(ctx, "SELECT count(*) FROM stats_daily").Scan(&n))
		return n
	}
	must(e.svc.Housekeeping(ctx))
	if count() != 1 {
		t.Fatal("a 400-day-old aggregate must stay")
	}
	e.clock.Advance(24 * time.Hour)
	must(e.svc.Housekeeping(ctx))
	if count() != 0 {
		t.Fatal("aggregates older than 400 days must be deleted")
	}
}

func TestCollectStatsDisabled(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.StatsLogFile = "" })
	if err := e.svc.CollectStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.svc.RunStats(ctx, time.Minute) // returns immediately when disabled
}

func TestCollectStatsSkipsPreviews(t *testing.T) {
	ctx := context.Background()
	logFile := filepath.Join(t.TempDir(), "sites-access.log")
	e := newEnv(t, wildcard, func(c *config.Config) { c.StatsLogFile = logFile })
	owner := e.user("owner@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	r := deploy(t, e, owner, v.Site.ID, "v1")
	preview := previewHost(v.Site.ID, r.Deploy.ID)
	if !e.hostExists(preview) {
		t.Fatal("preview host expected in wildcard mode")
	}
	now := e.clock.Now()
	appendLog(t, logFile,
		accessLine(now, preview, "/", "198.51.100.1", ua, ""),
		accessLine(now, "promo."+testBase, "/", "198.51.100.1", ua, "https://"+preview+"/"),
	)
	if err := e.svc.CollectStats(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.GetSiteStats(ctx, owner, "promo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Views != 1 || len(st.TopReferrers) != 0 {
		t.Fatalf("previews must not count as visits or sources: %+v", st)
	}
}
