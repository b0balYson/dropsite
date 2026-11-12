package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

func TestCreateSiteGetsRandomAddressAndServesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	v, err := e.svc.CreateSite(ctx, u, "  Промо ", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Site.Title != "Промо" || len(v.Site.ID) != 10 || v.URL != "https://"+v.Site.ID+"."+testBase+"/" {
		t.Fatalf("bad site %+v", v)
	}
	if _, err := e.served(v.Site.ID+"."+testBase, "index.html"); !os.IsNotExist(err) {
		t.Fatalf("site without releases must serve nothing, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "by-host", v.Site.ID+"."+testBase)); err != nil {
		t.Fatalf("default host must resolve to the empty dir for a cert and a 404: %v", err)
	}
}

func TestSlugLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	other := e.user("bob@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "promo-may")
	if site.Slug != "promo-may" || site.URL != "https://promo-may.pp.test/" || len(site.URLs) != 2 {
		t.Fatalf("slug not primary: %+v", site.URLs)
	}
	if _, err := e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "v1"}), domain.SourceCLI); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"promo-may.pp.test", site.Site.ID + ".pp.test"} {
		if body, err := e.served(h, "index.html"); err != nil || body != "v1" {
			t.Fatalf("%s: %q %v", h, body, err)
		}
	}
	if _, err := e.svc.CreateSite(ctx, other, "x", "promo-may"); err == nil {
		t.Fatal("taken slug must fail")
	} else {
		wantCode(t, err, domain.CodeSlugTaken)
	}
	if st, _ := e.svc.CheckSlugAvailable(ctx, other, "", "promo-may"); st.Available || st.Code != domain.CodeSlugTaken {
		t.Fatalf("availability %+v", st)
	}
	if st, _ := e.svc.CheckSlugAvailable(ctx, u, site.Site.ID, "promo-may"); !st.Available {
		t.Fatal("own current slug is available to its site")
	}
	if st, _ := e.svc.CheckSlugAvailable(ctx, u, "", site.Site.ID); st.Available {
		t.Fatal("a site id is never a free slug")
	}
	if st, _ := e.svc.CheckSlugAvailable(ctx, u, "", "admin"); st.Code != domain.CodeSlugReserved {
		t.Fatal("reserved")
	}
	for _, bad := range []string{"-ab", "a", "Promo", "a_b"} {
		s := bad
		if _, err := e.svc.UpdateSite(ctx, u, site.Site.ID, SiteUpdate{Slug: &s}); err == nil {
			t.Fatalf("slug %q must fail", bad)
		}
	}
	newSlug := "promo-june"
	v, err := e.svc.UpdateSite(ctx, u, "promo-may", SiteUpdate{Slug: &newSlug})
	if err != nil || v.Slug != "promo-june" {
		t.Fatalf("change slug: %v", err)
	}
	if _, err := e.served("promo-may.pp.test", "index.html"); !os.IsNotExist(err) {
		t.Fatal("old slug must stop serving at once")
	}
	if body, _ := e.served("promo-june.pp.test", "index.html"); body != "v1" {
		t.Fatal("new slug must serve")
	}
	if _, err := e.svc.CreateSite(ctx, other, "y", "promo-may"); err != nil {
		t.Fatalf("released slug must be free: %v", err)
	}
	empty := ""
	v, err = e.svc.UpdateSite(ctx, u, site.Site.ID, SiteUpdate{Slug: &empty})
	if err != nil || v.Slug != "" || len(v.URLs) != 1 {
		t.Fatalf("remove slug: %v %+v", err, v.URLs)
	}
	if _, err := e.served("promo-june.pp.test", "index.html"); !os.IsNotExist(err) {
		t.Fatal("removed slug must stop serving")
	}
}

func TestOwnershipIsolation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user("a@pp.test")
	b := e.user("b@pp.test")
	site, _ := e.svc.CreateSite(ctx, b, "B", "b-site")
	if _, err := e.svc.Deploy(ctx, b, site.Site.ID, zipOf(t, map[string]string{"index.html": "b"}), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	title := "hacked"
	ops := map[string]func(ref string) error{
		"get":    func(r string) error { _, err := e.svc.GetSite(ctx, a, r); return err },
		"update": func(r string) error { _, err := e.svc.UpdateSite(ctx, a, r, SiteUpdate{Title: &title}); return err },
		"deploy": func(r string) error {
			_, err := e.svc.Deploy(ctx, a, r, zipOf(t, map[string]string{"index.html": "x"}), domain.SourceAPI)
			return err
		},
		"deploys":   func(r string) error { _, _, err := e.svc.ListDeploys(ctx, a, r); return err },
		"rollback":  func(r string) error { _, err := e.svc.Rollback(ctx, a, r, ""); return err },
		"delete":    func(r string) error { return e.svc.DeleteSite(ctx, a, r) },
		"slugcheck": func(r string) error { _, err := e.svc.CheckSlugAvailable(ctx, a, r, "free-one"); return err },
		"apex":      func(r string) error { _, err := e.svc.SetApex(ctx, a, r); return err },
		"transfer":  func(r string) error { _, err := e.svc.TransferSite(ctx, a, r, a.User.ID); return err },
	}
	for name, op := range ops {
		for _, ref := range []string{site.Site.ID, "b-site"} {
			err := op(ref)
			wantNotFound(t, err)
			missing := op("zzzzzzzzzz")
			if err.Error() != missing.Error() {
				t.Fatalf("%s: foreign (%v) and missing (%v) differ", name, err, missing)
			}
		}
	}
	list, total, _ := e.svc.ListSites(ctx, a, Page{})
	if total != 0 || len(list) != 0 {
		t.Fatal("foreign sites must not be listed")
	}
	if body, _ := e.served("b-site.pp.test", "index.html"); body != "b" {
		t.Fatal("foreign site must stay untouched")
	}
	if _, err := e.svc.GetSite(ctx, e.admin, "b-site"); err != nil {
		t.Fatalf("admin sees every site: %v", err)
	}
	if _, err := e.svc.GetSite(ctx, a, "../etc"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("garbage refs are not found")
	}
}

func TestApexAndTransfer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	b := e.user("bob@pp.test")
	s1, _ := e.svc.CreateSite(ctx, u, "one", "")
	s2, _ := e.svc.CreateSite(ctx, u, "two", "")
	_, _ = e.svc.Deploy(ctx, u, s1.Site.ID, zipOf(t, map[string]string{"index.html": "one"}), domain.SourceUI)
	_, _ = e.svc.Deploy(ctx, u, s2.Site.ID, zipOf(t, map[string]string{"index.html": "two"}), domain.SourceUI)
	if _, err := e.svc.SetApex(ctx, u, s1.Site.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("apex is admin only")
	}
	v, err := e.svc.SetApex(ctx, e.admin, s1.Site.ID)
	if err != nil || len(v.URLs) != 2 {
		t.Fatalf("apex: %v %+v", err, v.URLs)
	}
	if body, _ := e.served(testBase, "index.html"); body != "one" {
		t.Fatal("apex must serve site one")
	}
	if _, err := e.svc.SetApex(ctx, e.admin, s2.Site.ID); err != nil {
		t.Fatal(err)
	}
	if body, _ := e.served(testBase, "index.html"); body != "two" {
		t.Fatal("apex must move to site two")
	}
	if v, _ := e.svc.GetSite(ctx, e.admin, s1.Site.ID); len(v.URLs) != 1 {
		t.Fatal("site one lost the apex")
	}
	if _, err := e.svc.ClearApex(ctx, e.admin, s2.Site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.served(testBase, "index.html"); !os.IsNotExist(err) {
		t.Fatal("cleared apex must not serve")
	}

	if _, err := e.svc.TransferSite(ctx, e.admin, s1.Site.ID, b.User.ID); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := e.svc.ListSites(ctx, b, Page{}); len(list) != 1 || list[0].ID != s1.Site.ID {
		t.Fatal("transferred site must appear for the new owner")
	}
	if list, _, _ := e.svc.ListSites(ctx, u, Page{}); len(list) != 1 {
		t.Fatal("transferred site must disappear for the old owner")
	}
	if body, _ := e.served(s1.Site.ID+".pp.test", "index.html"); body != "one" {
		t.Fatal("addresses survive a transfer")
	}
	_, _ = e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true)
	if _, err := e.svc.TransferSite(ctx, e.admin, s1.Site.ID, u.User.ID); err == nil {
		t.Fatal("cannot transfer to a blocked user")
	}
	if _, err := e.svc.TransferSite(ctx, e.admin, s1.Site.ID, 9999); err == nil {
		t.Fatal("cannot transfer to a missing user")
	}
	all, total, _ := e.svc.ListAllSites(ctx, e.admin, "bob@", Page{})
	if total != 1 || len(all) != 1 {
		t.Fatalf("admin search by owner: %d", total)
	}
}

func TestSoftDeleteAndPurge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "promo-may")
	_, _ = e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "v1"}), domain.SourceUI)
	if err := e.svc.DeleteSite(ctx, u, "promo-may"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"promo-may.pp.test", site.Site.ID + ".pp.test"} {
		if _, err := e.served(h, "index.html"); !os.IsNotExist(err) {
			t.Fatalf("%s must stop at once", h)
		}
	}
	if list, _, _ := e.svc.ListSites(ctx, u, Page{}); len(list) != 0 {
		t.Fatal("deleted site must not be listed")
	}
	if _, err := e.svc.CreateSite(ctx, u, "again", "promo-may"); err != nil {
		t.Fatalf("slug must be free at once: %v", err)
	}
	siteDir := filepath.Join(e.root, "sites", site.Site.ID)
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(siteDir); err != nil {
		t.Fatal("files kept during retention")
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(siteDir); err != nil {
		t.Fatal("reconcile must keep files of soft-deleted sites")
	}
	e.clock.Advance(DeletedSiteTTL + time.Minute)
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(siteDir); !os.IsNotExist(err) {
		t.Fatal("files must be purged after 7 days")
	}
	if ds, _ := e.repo.ListDeploys(ctx, site.Site.ID); len(ds) != 0 {
		t.Fatal("deploy rows must be purged")
	}
	if used, _ := e.repo.SiteIDExists(ctx, site.Site.ID); !used {
		t.Fatal("tombstone keeps the id from being reissued")
	}
}

func TestSiteLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	for i := range 10 {
		if _, err := e.svc.CreateSite(ctx, u, "s", ""); err != nil {
			t.Fatalf("site %d: %v", i, err)
		}
	}
	_, err := e.svc.CreateSite(ctx, u, "s", "")
	wantCode(t, err, domain.CodeSiteLimit)
	if de := domain.AsError(err); de.Params["limit"] != 10 {
		t.Fatal("error must name the limit")
	}
	list, _, _ := e.svc.ListSites(ctx, u, Page{})
	if err := e.svc.DeleteSite(ctx, u, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSite(ctx, u, "s", ""); err != nil {
		t.Fatal("deleted sites do not count")
	}
}

func TestTitleUpdateAndListing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Old", "")
	newTitle := "New title"
	v, err := e.svc.UpdateSite(ctx, u, site.Site.ID, SiteUpdate{Title: &newTitle})
	if err != nil || v.Site.Title != "New title" {
		t.Fatalf("%v %+v", err, v.Site)
	}
	bad := ""
	if _, err := e.svc.UpdateSite(ctx, u, site.Site.ID, SiteUpdate{Title: &bad}); err == nil {
		t.Fatal("empty title must fail")
	}
	_, _ = e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "hello"}), domain.SourceUI)
	list, _, _ := e.svc.ListSites(ctx, u, store.Page{})
	if len(list) != 1 || list[0].LastDeployAt == nil || list[0].SizeBytes != 5 || list[0].URL == "" {
		t.Fatalf("summary %+v", list)
	}
}

func TestConcurrentSlugClaims(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := range 8 {
		u := e.user("u" + string(rune('a'+i)) + "@pp.test")
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.CreateSite(ctx, u, "race", "hot-slug"); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one site may win the slug, got %d", wins)
	}
}
