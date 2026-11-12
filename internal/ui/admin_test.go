package ui

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

var adminPages = []string{"/admin/users", "/admin/sites", "/admin/audit", "/admin/settings"}

func TestAdminPagesHiddenFromUsers(t *testing.T) {
	h := newHarness(t)
	h.loginUser("alice@pp.test")
	notFound := h.get("/definitely/missing")
	wantStatus(t, notFound, http.StatusNotFound)
	for _, path := range adminPages {
		p := h.get(path)
		wantStatus(t, p, http.StatusNotFound)
		if strings.ReplaceAll(p.body, path, "/definitely/missing") != notFound.body {
			t.Errorf("%s: 404 page differs from a missing page", path)
		}
	}
	tok := h.token("/")
	for _, path := range []string{"/admin/users", "/admin/settings", "/admin/users/1/block", "/admin/sites/x/apex"} {
		wantStatus(t, h.post(path, url.Values{csrfField: {tok}, "email": {"x@pp.test"}}), http.StatusNotFound)
	}
	home := h.get("/")
	wantNotContains(t, home.body, `href="/admin/users"`, `href="/admin/settings"`)
	wantContains(t, home.body, `href="/connect"`)
}

func TestAdminSeesAdminPages(t *testing.T) {
	h := newHarness(t)
	h.loginAdmin()
	home := h.get("/")
	wantContains(t, home.body, `href="/admin/users"`, `href="/admin/audit"`)
	for _, path := range adminPages {
		wantStatus(t, h.get(path), http.StatusOK)
	}
}

var inviteRe = regexp.MustCompile(`http://127\.0\.0\.1:8082/invite/([A-Za-z0-9_-]+)`)

func TestAdminUsers(t *testing.T) {
	h := newHarness(t)
	h.loginAdmin()
	p := h.submit("/admin/users", "/admin/users", url.Values{"email": {"bob@pp.test"}, "role": {"user"}})
	wantStatus(t, p, http.StatusOK)
	m := inviteRe.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("invite link not shown:\n%.1500s", p.body)
	}
	wantContains(t, p.body, "Ссылка для bob@pp.test", "приглашён", "Скопировать")
	if _, err := h.env.Svc.InviteUser(context.Background(), m[1]); err != nil {
		t.Fatalf("shown invite does not work: %v", err)
	}

	dup := h.submit("/admin/users", "/admin/users", url.Values{"email": {"bob@pp.test"}})
	wantStatus(t, dup, http.StatusConflict)
	wantContains(t, dup.body, "уже есть")
	badEmail := h.submit("/admin/users", "/admin/users", url.Values{"email": {"nope"}})
	wantContains(t, badEmail.body, "не похоже на адрес почты")

	bob := findUser(t, h, "bob@pp.test")
	id := strconv.FormatInt(bob.ID, 10)
	re := h.submit("/admin/users", "/admin/users/"+id+"/invite", nil)
	wantStatus(t, re, http.StatusOK)
	if m2 := inviteRe.FindStringSubmatch(re.body); m2 == nil || m2[1] == m[1] {
		t.Fatal("reissued invite missing or identical")
	}

	blk := h.follow(h.submit("/admin/users", "/admin/users/"+id+"/block", nil))
	wantContains(t, blk.body, "bob@pp.test заблокирован", "Разблокировать")
	if u := findUser(t, h, "bob@pp.test"); u.Status != domain.UserBlocked {
		t.Fatalf("status = %s", u.Status)
	}
	reBlocked := h.follow(h.submit("/admin/users", "/admin/users/"+id+"/invite", nil))
	wantContains(t, reBlocked.body, "Сначала разблокируйте")
	unb := h.follow(h.submit("/admin/users", "/admin/users/"+id+"/unblock", nil))
	wantContains(t, unb.body, "bob@pp.test разблокирован")

	self := h.follow(h.submit("/admin/users", "/admin/users/"+strconv.FormatInt(h.env.Admin.User.ID, 10)+"/block", nil))
	wantContains(t, self.body, "своей учётной записью")
	wantStatus(t, h.submit("/admin/users", "/admin/users/0/block", nil), http.StatusNotFound)
	wantStatus(t, h.follow(h.submit("/admin/users", "/admin/users/9999/block", nil)), http.StatusNotFound)

	search := h.get("/admin/users?q=bob")
	wantContains(t, search.body, "bob@pp.test")
	wantNotContains(t, search.body, `addr-break">admin@pp.test</td>`)
}

func findUser(t *testing.T, h *harness, email string) service.UserSummary {
	t.Helper()
	users, _, err := h.env.Svc.ListUsers(context.Background(), h.env.Admin, service.UserFilter{Query: email})
	if err != nil || len(users) != 1 {
		t.Fatalf("find %s: %v (%d)", email, err, len(users))
	}
	return users[0]
}

func TestAdminSites(t *testing.T) {
	h := newHarness(t)
	alice := h.env.User("alice@pp.test")
	h.env.User("bob@pp.test")
	a := h.createSite(alice, "Alpha landing", "alpha")
	h.createSite(alice, "Beta", "")
	h.loginAdmin()

	all := h.get("/admin/sites")
	wantContains(t, all.body, "Alpha landing", "Beta", "alice@pp.test", "всего 2")
	search := h.get("/admin/sites?q=alph")
	wantContains(t, search.body, "Alpha landing")
	wantNotContains(t, search.body, ">Beta<")
	byOwner := h.get("/admin/sites?q=alice")
	wantContains(t, byOwner.body, "Alpha landing", "Beta")
	none := h.get("/admin/sites?q=zzz")
	wantContains(t, none.body, "ничего не найдено")

	path := "/admin/sites/" + a.Site.ID
	noConfirm := h.follow(h.submit("/admin/sites", path+"/apex", url.Values{"back": {"/admin/sites?q=alph"}}))
	wantContains(t, noConfirm.body, "Поставьте галочку")
	set := h.submit("/admin/sites", path+"/apex", url.Values{"confirm": {"yes"}, "back": {"/admin/sites?q=alph"}})
	wantStatus(t, set, http.StatusSeeOther)
	if set.location() != "/admin/sites?q=alph" {
		t.Fatalf("back = %q", set.location())
	}
	wantContains(t, h.follow(set).body, "открывается по адресу pp.test", "корень домена", "https://pp.test/")
	clr := h.follow(h.submit("/admin/sites", path+"/apex/clear", url.Values{"confirm": {"yes"}, "back": {"https://evil.example"}}))
	wantContains(t, clr.body, "Корень домена отвязан")

	tr := h.follow(h.submit("/admin/sites", path+"/transfer", url.Values{"email": {"BOB@pp.test"}}))
	wantContains(t, tr.body, "передан пользователю bob@pp.test")
	v, err := h.env.Svc.GetSite(context.Background(), h.env.Admin, a.Site.ID)
	if err != nil || v.OwnerID != findUser(t, h, "bob@pp.test").ID {
		t.Fatalf("owner not changed: %v", err)
	}
	nobody := h.follow(h.submit("/admin/sites", path+"/transfer", url.Values{"email": {"ghost@pp.test"}}))
	wantContains(t, nobody.body, "не найден")
	missing := h.submit("/admin/sites", "/admin/sites/nosuchsite/apex", url.Values{"confirm": {"yes"}})
	wantStatus(t, missing, http.StatusNotFound)
}

func TestAdminAudit(t *testing.T) {
	h := newHarness(t)
	alice := h.env.User("alice@pp.test")
	v := h.createSite(alice, "Logged", "")
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "x"})
	h.loginAdmin()

	all := h.get("/admin/audit")
	wantStatus(t, all, http.StatusOK)
	wantContains(t, all.body, "Создан сайт", "Опубликован релиз", "site.create", "alice@pp.test", "title", "Logged")

	id := strconv.FormatInt(alice.User.ID, 10)
	filtered := h.get("/admin/audit?action=deploy.create&user=" + id + "&from=2026-11-12&to=2026-11-12")
	wantContains(t, filtered.body, "Опубликован релиз")
	wantNotContains(t, filtered.body, "<strong>Создан сайт</strong>")
	wantContains(t, filtered.body, `value="2026-11-12"`)

	future := h.get("/admin/audit?from=2030-01-01")
	wantContains(t, future.body, "Записей с такими условиями нет")
	junk := h.get("/admin/audit?user=abc&from=yesterday&page=-3")
	wantStatus(t, junk, http.StatusOK)
}

func TestSettingsApplyImmediately(t *testing.T) {
	h := newHarness(t)
	h.loginAdmin()
	page := h.get("/admin/settings")
	wantContains(t, page.body, "по умолчанию", "Значение по умолчанию")

	p := h.submit("/admin/settings", "/admin/settings", url.Values{
		"max_sites_per_user": {"1"}, "max_site_bytes": {"25"}, "reserved_slugs": {"promo\nsale"},
	})
	wantStatus(t, p, http.StatusSeeOther)
	saved := h.follow(p)
	wantContains(t, saved.body, "Настройки сохранены", "изменено", `value="25"`, "promo\nsale")
	lim, _ := h.env.Svc.Limits(context.Background())
	if lim.MaxSitesPerUser != 1 || lim.MaxSiteBytes != 25*mib {
		t.Fatalf("limits = %+v", lim)
	}

	user := newHarnessSharing(t, h)
	user.loginUser("carol@pp.test")
	wantStatus(t, user.submit("/", "/sites", url.Values{"title": {"One"}}), http.StatusSeeOther)
	second := user.submit("/", "/sites", url.Values{"title": {"Two"}})
	wantStatus(t, second, http.StatusForbidden)
	wantContains(t, second.body, "Достигнут лимит сайтов (1)")
	wantContains(t, user.get("/").body, "Вы создали максимум сайтов (1)")
	reserved := user.get("/slug-check?slug=sale")
	wantContains(t, reserved.body, "зарезервирован")

	reset := h.follow(h.submit("/admin/settings", "/admin/settings", url.Values{
		"reset_max_sites_per_user": {"1"}, "reset_reserved_slugs": {"1"}, "max_site_bytes": {"25"},
	}))
	wantContains(t, reset.body, "Настройки сохранены")
	st, _ := h.env.Svc.GetSettings(context.Background(), h.env.Admin)
	if st.Overridden[service.SettingMaxSitesPerUser] || st.Overridden[service.SettingReservedSlugs] || !st.Overridden[service.SettingMaxSiteBytes] {
		t.Fatalf("overrides after reset: %v", st.Overridden)
	}

	notNum := h.follow(h.submit("/admin/settings", "/admin/settings", url.Values{"max_files": {"lots"}}))
	wantContains(t, notNum.body, "должно быть числом")
	tooFew := h.follow(h.submit("/admin/settings", "/admin/settings", url.Values{"keep_releases": {"1"}}))
	wantContains(t, tooFew.body, "Хранить релизов")
	badSlug := h.follow(h.submit("/admin/settings", "/admin/settings", url.Values{"reserved_slugs": {"Bad_Slug!"}}))
	wantContains(t, badSlug.body, "Зарезервированные адреса")
	if !strings.Contains(badSlug.body, "notice--error") {
		t.Fatal("invalid slug not reported as an error")
	}
}

// newHarnessSharing is a second browser against the same server.
func newHarnessSharing(t *testing.T, h *harness) *harness {
	t.Helper()
	other := *h
	other.t = t
	other.c = h.newClient()
	return &other
}
