package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const page = "<!doctype html><html><head><title>Привет</title></head><body>x</body></html>"

func TestCountersInjectedOnDeploy(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.user("owner@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetSiteCounters(ctx, owner, "promo", domain.Counters{MetrikaID: "12345678"}, domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{Counters: &domain.Counters{GA4ID: "g-glob4l"}}); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Deploy(ctx, owner, "promo", zipOf(t, map[string]string{
		"index.html": page, "docs/a.htm": "<p>a</p>", "app.css": "</head>",
	}), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
	host := v.Site.ID + "." + testBase
	index, _ := e.served(host, "index.html")
	for _, want := range []string{"ym(12345678", `gtag("config","G-GLOB4L")`, "Привет"} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html lacks %q:\n%s", want, index)
		}
	}
	if a, _ := e.served(host, "docs/a.htm"); !strings.Contains(a, "ym(12345678") {
		t.Error("nested .htm lacks the snippet")
	}
	if css, _ := e.served(host, "app.css"); css != "</head>" {
		t.Errorf("css changed: %q", css)
	}
	if res.Deploy.SizeBytes <= int64(len(page)+len("<p>a</p>")+len("</head>")) {
		t.Errorf("size %d must include the snippet", res.Deploy.SizeBytes)
	}
	got, err := e.svc.GetSiteCounters(ctx, owner, "promo")
	if err != nil || got.Own.MetrikaID != "12345678" || got.Global.GA4ID != "G-GLOB4L" {
		t.Fatalf("counters %+v, %v", got, err)
	}
}

func TestSetCountersCreatesReleaseCopy(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.user("owner@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "")
	if err != nil {
		t.Fatal(err)
	}
	// Without a release only the setting is stored.
	d, err := e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{GA4ID: "G-FIRST1"}, domain.SourceUI)
	if err != nil || d != nil {
		t.Fatalf("no release: %v, %v", d, err)
	}
	first, err := e.svc.Deploy(ctx, owner, v.Site.ID, zipOf(t, map[string]string{"index.html": page, "logo.svg": "<svg/>"}), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
	host := v.Site.ID + "." + testBase
	logo := filepath.Join(e.root, "by-host", host, "logo.svg")
	logoBefore, _ := os.Stat(logo)

	e.clock.Advance(time.Minute)
	d, err = e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{MetrikaID: "87654321"}, domain.SourceUI)
	if err != nil || d == nil || d.ID == first.Deploy.ID {
		t.Fatalf("release copy: %+v, %v", d, err)
	}
	index, _ := e.served(host, "index.html")
	if !strings.Contains(index, "ym(87654321") || strings.Contains(index, "G-FIRST1") ||
		strings.Count(index, "dropsite:counters") != 2 {
		t.Fatalf("index after change:\n%s", index)
	}
	logoAfter, _ := os.Stat(logo)
	if !logoAfter.ModTime().Equal(logoBefore.ModTime()) {
		t.Error("unchanged files must keep their mtime (ETag)")
	}
	cur, _ := e.svc.GetSite(ctx, owner, v.Site.ID)
	if cur.Current == nil || cur.Current.ID != d.ID || d.FileCount != 2 || d.Source != domain.SourceUI {
		t.Fatalf("current %+v, deploy %+v", cur.Current, d)
	}
	// Same value again: nothing happens.
	if again, err := e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{MetrikaID: "87654321"}, domain.SourceUI); err != nil || again != nil {
		t.Fatalf("unchanged: %v, %v", again, err)
	}
	// Rollback brings the previous snippet back.
	if _, err := e.svc.Rollback(ctx, owner, v.Site.ID, first.Deploy.ID); err != nil {
		t.Fatal(err)
	}
	if index, _ := e.served(host, "index.html"); !strings.Contains(index, "G-FIRST1") {
		t.Fatal("rollback must restore the old release")
	}
	if _, err := e.svc.Rollback(ctx, owner, v.Site.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	// Clearing removes the block entirely.
	e.clock.Advance(time.Minute)
	if _, err := e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{}, domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	if index, _ := e.served(host, "index.html"); index != page {
		t.Fatalf("after clearing:\n%s", index)
	}
	rows, _, err := e.svc.ListAudit(ctx, e.admin, AuditFilter{Action: "site.counters"})
	if err != nil || len(rows) != 3 {
		t.Fatalf("audit %d, %v", len(rows), err)
	}
}

func TestSetCountersValidationAndAccess(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.user("owner@pp.test")
	stranger := e.user("stranger@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{GA4ID: `G-1"><script>`}, domain.SourceUI)
	wantCode(t, err, domain.CodeCounterInvalid)
	_, err = e.svc.SetSiteCounters(ctx, stranger, v.Site.ID, domain.Counters{MetrikaID: "12345678"}, domain.SourceUI)
	wantNotFound(t, err)
	_, err = e.svc.GetSiteCounters(ctx, stranger, v.Site.ID)
	wantNotFound(t, err)
	_, err = e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{Counters: &domain.Counters{MetrikaID: "abc"}})
	wantCode(t, err, domain.CodeCounterInvalid)
	// Clearing global counters resets the settings.
	if _, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{Counters: &domain.Counters{MetrikaID: "1234"}}); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{Counters: &domain.Counters{}})
	if err != nil || !st.Counters.Empty() || st.Overridden[SettingCounterMetrika] {
		t.Fatalf("cleared %+v, %v", st, err)
	}
}

func TestSetCountersRateLimited(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	owner := e.user("owner@pp.test")
	v, err := e.svc.CreateSite(ctx, owner, "Promo", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Deploy(ctx, owner, v.Site.ID, zipOf(t, map[string]string{"index.html": page}), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	for i := range DeploysPerHour - 1 {
		e.clock.Advance(time.Second)
		id := []string{"1000", "2000"}[i%2]
		if _, err := e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{MetrikaID: id}, domain.SourceUI); err != nil {
			t.Fatalf("change %d: %v", i, err)
		}
	}
	_, err = e.svc.SetSiteCounters(ctx, owner, v.Site.ID, domain.Counters{MetrikaID: "3000"}, domain.SourceUI)
	wantCode(t, err, domain.CodeRateLimited)
}
