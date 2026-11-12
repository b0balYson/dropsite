package ui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestSiteAccessForm(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Docs", "docs")
	site := "/sites/" + v.Site.ID
	page := h.get(site)
	wantContains(t, page.body, "Доступ", `name="noindex"`, `name="protected"`, `name="expires"`)

	// Protect, hide and expire in one go.
	day := h.env.Clock.Now().Add(48 * time.Hour).Format("2006-01-02")
	p := h.submit(site, site+"/access", url.Values{
		"noindex": {"1"}, "protected": {"1"}, "password": {"visitor-pass-1"}, "expires": {day},
	})
	wantStatus(t, p, http.StatusSeeOther)
	got, err := h.env.Svc.GetSite(context.Background(), alice, v.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Site.Noindex || !got.Site.Protected || got.Site.ExpiresAt == nil ||
		got.Site.ExpiresAt.Format("2006-01-02") != h.env.Clock.Now().Add(72*time.Hour).Format("2006-01-02") {
		t.Fatalf("site %+v", got.Site)
	}
	page = h.get(site)
	wantContains(t, page.body, "Настройки доступа сохранены", "пароль", "скрыт от поиска",
		`value="`+day+`"`, "notice--warn", "Новый пароль")
	wantNotContains(t, page.body, "visitor-pass-1")
	list := h.get("/")
	wantContains(t, list.body, "пароль", "скрыт от поиска", "badge--warn")

	// Unticking without a password keeps nothing secret around and opens the site.
	p = h.submit(site, site+"/access", url.Values{"expires": {""}})
	wantStatus(t, p, http.StatusSeeOther)
	got, _ = h.env.Svc.GetSite(context.Background(), alice, v.Site.ID)
	if got.Site.Noindex || got.Site.Protected || got.Site.ExpiresAt != nil {
		t.Fatalf("after clearing %+v", got.Site)
	}

	// Protecting without a password is explained, nothing changes.
	p = h.submit(site, site+"/access", url.Values{"protected": {"1"}})
	wantContains(t, h.follow(p).body, "задайте пароль")
	bad := h.submit(site, site+"/access", url.Values{"expires": {"not-a-date"}})
	wantContains(t, h.follow(bad).body, "Не удалось разобрать дату")

	// Another user cannot touch it.
	h.c = h.newClient()
	h.loginUser("bob@pp.test")
	p = h.submit("/", site+"/access", url.Values{"noindex": {"1"}})
	if p.resp.StatusCode != http.StatusNotFound && p.location() != "/" {
		t.Fatalf("foreign access form: %d %q", p.resp.StatusCode, p.location())
	}
	got, _ = h.env.Svc.GetSite(context.Background(), alice, v.Site.ID)
	if got.Site.Noindex {
		t.Fatal("foreign user changed the site")
	}
}

func TestUserLimitsScreen(t *testing.T) {
	h := newHarness(t)
	bob := h.env.User("bob@pp.test")
	h.loginAdmin()
	path := "/admin/users/" + strconv.FormatInt(bob.User.ID, 10) + "/limits"
	users := h.get("/admin/users")
	wantContains(t, users.body, path, "Лимиты")
	page := h.get(path)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page.body, "Персональные лимиты", "как у всех")

	p := h.submit(path, path, url.Values{"max_site_bytes": {"100"}, "max_sites_per_user": {"20"}})
	wantStatus(t, p, http.StatusSeeOther)
	ul, err := h.env.Svc.GetUserLimits(context.Background(), h.env.Admin, bob.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ul.Effective.MaxSiteBytes != 100<<20 || ul.Effective.MaxSitesPerUser != 20 || ul.Overrides.MaxFiles != nil {
		t.Fatalf("limits %+v", ul)
	}
	page = h.follow(p)
	wantContains(t, page.body, "Лимиты пользователя bob@pp.test сохранены", "персональный")

	bad := h.submit(path, path, url.Values{"keep_releases": {"1"}})
	wantContains(t, h.follow(bad).body, "notice--error")
	p = h.submit(path, path, url.Values{"max_site_bytes": {"100"}, "reset_max_site_bytes": {"1"}})
	wantStatus(t, p, http.StatusSeeOther)
	ul, _ = h.env.Svc.GetUserLimits(context.Background(), h.env.Admin, bob.User.ID)
	if !ul.Overrides.Empty() {
		t.Fatalf("reset left %+v", ul.Overrides)
	}

	// Users get "not found".
	h.c = h.newClient()
	h.loginUser("carol@pp.test")
	wantStatus(t, h.get(path), http.StatusNotFound)
}
