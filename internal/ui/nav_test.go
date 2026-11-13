package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSiteSectionsHaveOwnPages(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "promo")
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	base := "/sites/" + v.Site.ID

	cases := []struct {
		path, heading string
	}{
		{base, "Опубликовать новую версию"},
		{base + "/versions", "История релизов"},
		{base + "/forms", "Куда отправлять"},
		{base + "/data", "Коллекции"},
		{base + "/stats", "Посещаемость"},
		{base + "/access", "Свои домены"},
		{base + "/proxy", "Ключи API"},
		{base + "/settings", "Название и адрес"},
	}
	for _, c := range cases {
		p := h.get(c.path)
		wantStatus(t, p, http.StatusOK)
		// The shared header and the sidebar's site group are on every section.
		wantContains(t, p.body, c.heading, `class="side-site"`, `title="Promo"`, "promo.<wbr>pp.<wbr>test",
			`href="`+c.path+`" aria-current="page"`, `class="badge badge--current"`)
		if n := strings.Count(p.body, `aria-current="page"`); n != 1 {
			t.Errorf("%s: %d links marked as the current page, want 1", c.path, n)
		}
		// The site group sits under "Sites", which marks the location.
		wantContains(t, p.body, `href="/" aria-current="true"`)
	}
	// Only the overview names the site in its h1; sections have their own.
	wantContains(t, h.get(base).body, `<h1 class="display display--site">Promo</h1>`)
	wantContains(t, h.get(base+"/access").body, `<h1 class="display display--section">Доступ и домены</h1>`)
}

func TestNestedSectionPageMarksItsSection(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Cafe", "")
	base := "/sites/" + v.Site.ID + "/data"
	h.follow(h.submit(base, base, url.Values{"name": {"menu"}, "mode": {"public"}}))
	p := h.get(base + "/menu")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, `href="`+base+`" aria-current="true"`, `href="`+base+`">← Данные`)
	wantNotContains(t, p.body, `aria-current="page"><span class="side-link__text">`)
}

func TestSidebarAdminGroupOnlyForAdmins(t *testing.T) {
	h := newHarness(t)
	h.loginUser("alice@pp.test")
	user := h.get("/workspaces")
	wantContains(t, user.body, `href="/workspaces" aria-current="page"`, `href="/connect"`, "alice@pp.test",
		`aria-controls="sidebar"`, `aria-expanded="false"`, `id="sidebar"`)
	wantNotContains(t, user.body, "/admin/users", "Администрирование", `class="side-site"`)

	h.c = h.newClient()
	h.loginAdmin()
	admin := h.get("/admin/audit")
	wantContains(t, admin.body, "Администрирование", `href="/admin/users"`, `href="/admin/sites"`,
		`href="/admin/domains"`, `href="/admin/settings"`, `href="/admin/audit" aria-current="page"`)
	// The compact top bar names the current section.
	wantContains(t, admin.body, `<span class="topbar__section">Журнал</span>`)
}

func TestSignInPageHasNoSidebar(t *testing.T) {
	h := newHarness(t)
	p := h.get("/login")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, `class="masthead"`, `name="lang"`)
	wantNotContains(t, p.body, `id="sidebar"`, "topbar")
}

func TestPostsReturnToTheirSection(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "promo")
	first := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "two"})
	base := "/sites/" + v.Site.ID

	cases := []struct {
		path string
		form url.Values
		want string
	}{
		{base + "/settings", url.Values{"title": {"Promo 2"}, "slug": {"promo"}}, base + "/settings"},
		{base + "/access", url.Values{"noindex": {"1"}}, base + "/access"},
		{base + "/counters", url.Values{"metrika_id": {"12345678"}}, base + "/stats"},
		{base + "/rollback", url.Values{"deploy": {first.ID}}, base + "/versions"},
		{base + "/domains", url.Values{"domain": {"promo.example.ru"}}, base + "/access#domains-h"},
		{base + "/delete", url.Values{"confirm": {"nope"}}, base + "/settings#delete-h"},
		{base + "/move", url.Values{"workspace": {"0"}}, base + "/settings"},
		{base + "/schedules", url.Values{"deploy": {first.ID}, "publish_at": {"bad"}}, base + "/versions"},
	}
	for _, c := range cases {
		p := h.submit(base, c.path, c.form)
		wantStatus(t, p, http.StatusSeeOther)
		if p.location() != c.want {
			t.Errorf("POST %s redirected to %q, want %q", c.path, p.location(), c.want)
		}
	}
	// The flash shows on the section the form lives in.
	p := h.follow(h.submit(base, base+"/access", url.Values{}))
	wantContains(t, p.body, "Настройки доступа сохранены", `href="`+base+`/access" aria-current="page"`)
}

func TestOverviewSummarisesAndListsRecentReleases(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, h.deploy(alice, v.Site.ID, map[string]string{"index.html": strings.Repeat("x", i+1)}).ID)
	}
	base := "/sites/" + v.Site.ID
	p := h.get(base)
	wantContains(t, p.body, ids[4], ids[2], `href="`+base+`/versions"`, `href="`+base+`/forms"`,
		`href="`+base+`/data"`, `href="`+base+`/proxy"`, `href="`+base+`/access#domains-h"`,
		`data-history="`+base+`/history?recent=1"`, "data-site-overview")
	wantNotContains(t, p.body, ids[1], ids[0])

	recent := h.get(base + "/history?recent=1")
	wantContains(t, recent.body, ids[4], `id="site-meta" hx-swap-oob="true"`)
	wantNotContains(t, recent.body, ids[1])
	all := h.get(base + "/history")
	wantContains(t, all.body, ids[0], ids[4])
	wantContains(t, h.get(base+"/versions").body, ids[0], ids[4])
}

func TestSiteSectionsHideOthersSites(t *testing.T) {
	h := newHarness(t)
	bob := h.env.User("bob@pp.test")
	v := h.createSite(bob, "Bob site", "")
	h.loginUser("alice@pp.test")
	for _, s := range []string{"versions", "stats", "access", "settings"} {
		wantStatus(t, h.get("/sites/"+v.Site.ID+"/"+s), http.StatusNotFound)
	}
}
