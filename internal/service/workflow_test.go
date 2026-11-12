package service

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

func (e *env) auditCount(action string) int {
	e.t.Helper()
	_, n, err := e.repo.ListAudit(context.Background(), store.AuditFilter{Action: action, Page: store.Page{Limit: 100}})
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestCloneSiteCopiesOnlyFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	if _, err := e.svc.SetSiteCounters(ctx, u, id, domain.Counters{MetrikaID: "12345678"}, domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	pw := "secret-password-1"
	if _, err := e.svc.UpdateSite(ctx, u, id, SiteUpdate{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	src, err := e.svc.GetSite(ctx, u, id)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := e.served(src.Hostnames[0].Hostname, "index.html"); !strings.Contains(body, "12345678") {
		t.Fatal("source has no counter block")
	}

	res, err := e.svc.CloneSite(ctx, u, id, "", "shop-two", domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	v := res.Site
	if v.Site.ID == id || v.Site.Title != "Shop (copy)" || v.Slug != "shop-two" || v.Site.Protected || v.Current == nil {
		t.Fatalf("copy: %+v", v)
	}
	if v.Current.FileCount != 4 {
		t.Fatalf("files %d", v.Current.FileCount)
	}
	body, err := e.served(v.Hostnames[0].Hostname, "index.html")
	if err != nil || strings.Contains(body, "12345678") || !strings.Contains(body, "Price 100") {
		t.Fatalf("copy index %q %v", body, err)
	}
	if css, _ := e.served(v.Hostnames[0].Hostname, "css/site.css"); css != shop["css/site.css"] {
		t.Fatalf("css %q", css)
	}
	if c, _ := e.svc.GetSiteCounters(ctx, u, v.Site.ID); c.Own.MetrikaID != "" {
		t.Fatalf("counters copied: %+v", c.Own)
	}
	if after, _ := e.svc.GetSite(ctx, u, id); after.Current == nil || after.Current.ID != src.Current.ID {
		t.Fatal("source changed")
	}
	if e.auditCount("site.clone") != 1 {
		t.Fatal("no site.clone audit")
	}
}

func TestCloneSiteErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, other := e.user("o@pp.test"), e.user("x@pp.test")
	id, _, _ := e.published(owner, shop)
	_, err := e.svc.CloneSite(ctx, other, id, "", "", domain.SourceAPI)
	wantNotFound(t, err)
	empty, err := e.svc.CreateSite(ctx, owner, "Empty", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CloneSite(ctx, owner, empty.Site.ID, "", "", domain.SourceAPI)
	wantCode(t, err, domain.CodeSiteNotPublished)
	if got := copyTitle(strings.Repeat("я", domain.MaxTitleLength)); len([]rune(got)) != domain.MaxTitleLength {
		t.Fatalf("long copy title has %d runes", len([]rune(got)))
	}
	// A failed publication leaves no site behind.
	_, err = e.svc.CloneSite(ctx, owner, id, "x", "bad slug!", domain.SourceAPI)
	if err == nil {
		t.Fatal("invalid slug accepted")
	}
	if n, _ := e.repo.CountActiveSites(ctx, owner.User.ID); n != 2 {
		t.Fatalf("sites %d", n)
	}
}

func TestCreateSiteFromTemplate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	if got := e.svc.ListTemplates("en"); len(got) != 5 || got[0].Name != "cafe" {
		t.Fatalf("templates %+v", got)
	}
	res, err := e.svc.CreateSiteFromTemplate(ctx, u, "cafe", "Кофе у реки", "", "ru", domain.SourceMCP)
	if err != nil {
		t.Fatal(err)
	}
	if res.Site.Site.Title != "Кофе у реки" || res.Deploy.Source != domain.SourceMCP {
		t.Fatalf("result %+v", res.Site.Site)
	}
	body, err := e.served(res.Site.Hostnames[0].Hostname, "index.html")
	if err != nil || !strings.Contains(body, "Кофе у реки") || !strings.Contains(body, `lang="ru"`) {
		t.Fatalf("index %v", err)
	}
	if _, err := e.served(res.Site.Hostnames[0].Hostname, "404.html"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateSiteFromTemplate(ctx, u, "shop", "", "", "", domain.SourceAPI)
	wantCode(t, err, domain.CodeTemplateNotFound)
	if e.auditCount("site.template") != 1 {
		t.Fatal("no audit")
	}
}
