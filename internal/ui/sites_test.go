package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

var testPage = service.Page{Limit: service.MaxPageSize}

func TestSitesListAndEmptyState(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	empty := h.get("/")
	wantStatus(t, empty, http.StatusOK)
	wantContains(t, empty.body, "Здесь пока пусто", "Создать сайт")

	a := h.createSite(alice, "Promo", "promo")
	h.createSite(alice, "Docs", "")
	h.deploy(alice, a.Site.ID, map[string]string{"index.html": strings.Repeat("x", 3000)})
	list := h.get("/")
	wantContains(t, list.body, "Promo", "Docs", "https://promo.pp.test/", "promo.pp.test",
		"https://"+a.Site.ID+".pp.test/", "2,9 КБ", "Сайтов: 2 из")
	if n := strings.Count(list.body, `class="site-row"`); n != 2 {
		t.Fatalf("site rows = %d, want 2", n)
	}
}

func TestCreateSiteForm(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	p := h.submit("/", "/sites", url.Values{"title": {"Landing"}, "slug": {" Spring "}})
	wantStatus(t, p, http.StatusSeeOther)
	page := h.follow(p)
	wantContains(t, page.body, "Сайт создан", "spring.pp.test", "Landing")

	bad := h.submit("/", "/sites", url.Values{"title": {"Other"}, "slug": {"spring"}})
	wantStatus(t, bad, http.StatusConflict)
	wantContains(t, bad.body, "Этот адрес уже занят", `value="Other"`)

	empty := h.submit("/", "/sites", url.Values{"title": {"  "}})
	wantStatus(t, empty, http.StatusUnprocessableEntity)
	wantContains(t, empty.body, "не длиннее 120 символов")
	sites, _, _ := h.env.Svc.ListSites(context.Background(), alice, testPage)
	if len(sites) != 1 {
		t.Fatalf("sites = %d", len(sites))
	}
}

func TestTitleIsEscaped(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, `<script>alert(1)</script>`, "")
	for _, path := range []string{"/", "/sites/" + v.Site.ID} {
		p := h.get(path)
		wantStatus(t, p, http.StatusOK)
		wantNotContains(t, p.body, "<script>alert(1)</script>")
		wantContains(t, p.body, "&lt;script&gt;alert(1)&lt;/script&gt;")
	}
}

func TestUserCannotSeeOthersSites(t *testing.T) {
	h := newHarness(t)
	bob := h.env.User("bob@pp.test")
	v := h.createSite(bob, "Bob site", "bobsite")
	d := h.deploy(bob, v.Site.ID, map[string]string{"index.html": "1"})
	h.loginUser("alice@pp.test")

	for _, path := range []string{"/sites/" + v.Site.ID, "/sites/bobsite", "/sites/" + v.Site.ID + "/history"} {
		p := h.get(path)
		wantStatus(t, p, http.StatusNotFound)
		wantContains(t, p.body, "Страница не найдена")
	}
	tok := h.token("/")
	for _, path := range []string{"/sites/" + v.Site.ID + "/rollback", "/sites/" + v.Site.ID + "/delete", "/sites/" + v.Site.ID + "/settings"} {
		p := h.post(path, url.Values{csrfField: {tok}, "deploy": {d.ID}, "confirm": {"Bob site"}, "title": {"pwned"}})
		wantStatus(t, p, http.StatusNotFound)
	}
	up := h.uploadWithToken(v.Site.ID, tok)
	wantStatus(t, up, http.StatusNotFound)
	got, err := h.env.Svc.GetSite(context.Background(), bob, v.Site.ID)
	if err != nil || got.Site.Title != "Bob site" {
		t.Fatalf("bob's site changed: %+v %v", got.Site, err)
	}
	if s := h.get("/sites/" + v.Site.ID + "/slug-check?slug=abc"); !strings.Contains(s.body, "Не найдено") {
		t.Errorf("slug check on foreign site: %q", s.body)
	}
}

func (h *harness) uploadWithToken(ref, tok string) page {
	h.t.Helper()
	body := "--b\r\nContent-Disposition: form-data; name=\"archive\"; filename=\"a.zip\"\r\n\r\nPK\r\n--b--\r\n"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/sites/"+ref+"/deploy", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	req.Header.Set(csrfHeader, tok)
	return h.do(req)
}

func TestSlugCheck(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Mine", "mine")
	h.createSite(h.env.Admin, "Taken", "taken")
	cases := []struct {
		path, want string
	}{
		{"/slug-check?slug=taken", "Этот адрес уже занят"},
		{"/slug-check?slug=www", "зарезервирован"},
		{"/slug-check?slug=A_B", "только латинские буквы"},
		{"/slug-check?slug=free-one", "free-one.pp.test"},
		{"/slug-check?slug=" + v.Site.ID, "Этот адрес уже занят"},
		{"/sites/" + v.Site.ID + "/slug-check?slug=mine", "свободен"},
		{"/slug-check?slug=mine", "Этот адрес уже занят"},
	}
	for _, c := range cases {
		p := h.get(c.path, "HX-Request", "true")
		wantStatus(t, p, http.StatusOK)
		wantContains(t, p.body, c.want)
		wantNotContains(t, p.body, "<html")
	}
	if p := h.get("/slug-check?slug="); strings.TrimSpace(p.body) != "" {
		t.Errorf("empty slug renders %q", p.body)
	}
}

func TestSiteSettingsAndDelete(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Old", "")
	path := "/sites/" + v.Site.ID

	p := h.submit(path, path+"/settings", url.Values{"title": {"New title"}, "slug": {"shiny"}})
	wantStatus(t, p, http.StatusSeeOther)
	page := h.follow(p)
	wantContains(t, page.body, "Изменения сохранены", "New title", "https://shiny.pp.test/")

	bad := h.follow(h.submit(path, path+"/settings", url.Values{"title": {"New title"}, "slug": {"api"}}))
	wantContains(t, bad.body, "зарезервирован")

	mismatch := h.follow(h.submit(path, path+"/delete", url.Values{"confirm": {"wrong"}}))
	wantContains(t, mismatch.body, "Сайт не удалён")

	del := h.submit(path, path+"/delete", url.Values{"confirm": {"shiny.pp.test"}})
	wantStatus(t, del, http.StatusSeeOther)
	if del.location() != "/" {
		t.Fatalf("delete redirect %q", del.location())
	}
	wantContains(t, h.get("/").body, "удалён")
	wantStatus(t, h.get(path), http.StatusNotFound)
}

func TestDeleteConfirmed(t *testing.T) {
	v := service.SiteView{URLs: []string{"https://x.pp.test:8443/"}, Slug: "x"}
	v.Site.ID, v.Site.Title = "abc123", "Title"
	for in, want := range map[string]bool{
		"Title": true, "abc123": true, "x": true, "x.pp.test:8443": true, "https://x.pp.test:8443/": true,
		"": false, "title2": false,
	} {
		if got := deleteConfirmed(v, in); got != want {
			t.Errorf("deleteConfirmed(%q) = %v", in, got)
		}
	}
}

func TestDeployThroughUpload(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "promo")

	p := h.upload(v.Site.ID, "site.zip", testenv.Zip(t, map[string]string{"index.html": "<h1>v1</h1>"}), true)
	wantStatus(t, p, http.StatusOK)
	var reply deployReply
	if err := json.Unmarshal([]byte(p.body), &reply); err != nil {
		t.Fatal(err)
	}
	if !reply.OK || reply.URL != "https://promo.pp.test/" || reply.DeployID == "" || len(reply.URLs) != 2 {
		t.Fatalf("reply = %+v", reply)
	}
	if got, err := h.env.Served("promo.pp.test", "index.html"); err != nil || got != "<h1>v1</h1>" {
		t.Fatalf("served %q %v", got, err)
	}
	hist := h.get("/sites/"+v.Site.ID+"/history", "HX-Request", "true")
	wantContains(t, hist.body, reply.DeployID, "через интерфейс", "текущий", `id="site-meta" hx-swap-oob="true"`)

	// Without JavaScript the form posts and redirects with a flash.
	h.env.Clock.Advance(2e9)
	form := h.upload(v.Site.ID, "site.tar.gz", testenv.TarGz(t, map[string]string{"index.html": "v2"}), false)
	wantStatus(t, form, http.StatusSeeOther)
	page := h.follow(form)
	wantContains(t, page.body, "Новая версия опубликована", `href="https://promo.pp.test/"`)
}

func TestDeployArchiveErrorsAreTranslated(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "keep"})
	host := v.Site.ID + ".pp.test"

	p := h.upload(v.Site.ID, "site.zip", testenv.Zip(t, map[string]string{"about.html": "x"}), true)
	wantStatus(t, p, http.StatusUnprocessableEntity)
	var reply deployReply
	_ = json.Unmarshal([]byte(p.body), &reply)
	if reply.OK || !strings.Contains(reply.Message, "нет файла index.html") {
		t.Fatalf("reply = %+v", reply)
	}
	if got, _ := h.env.Served(host, "index.html"); got != "keep" {
		t.Fatalf("site changed after failed deploy: %q", got)
	}

	form := h.follow(h.upload(v.Site.ID, "site.zip", []byte("not an archive"), false))
	wantContains(t, form.body, "Этот файл не zip и не tar.gz")

	h.submit("/", "/lang", url.Values{"lang": {"en"}})
	en := h.upload(v.Site.ID, "site.zip", testenv.Zip(t, map[string]string{"a/b.html": "x", "c.html": "y"}), true)
	_ = json.Unmarshal([]byte(en.body), &reply)
	if !strings.Contains(reply.Message, "There is no index.html at the root") {
		t.Fatalf("english reply = %+v", reply)
	}

	none := h.uploadWithToken(v.Site.ID, h.token("/"))
	wantStatus(t, none, http.StatusSeeOther) // tiny "PK" body is not an archive
	nf := h.follow(none)
	wantContains(t, nf.body, "neither zip nor tar.gz")
}

func TestDeployRequiresCSRFAndFile(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	body := "--b\r\nContent-Disposition: form-data; name=\"archive\"; filename=\"a.zip\"\r\n\r\nPK\r\n--b--\r\n"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/sites/"+v.Site.ID+"/deploy", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	wantStatus(t, h.do(req), http.StatusForbidden)

	tok := h.token("/")
	noFile := "--b\r\nContent-Disposition: form-data; name=\"_csrf\"\r\n\r\n" + tok + "\r\n--b--\r\n"
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/sites/"+v.Site.ID+"/deploy", strings.NewReader(noFile))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	req.Header.Set("Accept", "application/json")
	p := h.do(req)
	wantStatus(t, p, http.StatusUnprocessableEntity)
	wantContains(t, p.body, "Файл не выбран")

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/sites/"+v.Site.ID+"/deploy", strings.NewReader("x"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set(csrfHeader, tok)
	wantStatus(t, h.do(req), http.StatusSeeOther)
}

func TestRollbackFromHistory(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	first := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "two"})
	path := "/sites/" + v.Site.ID

	page := h.get(path)
	wantContains(t, page.body, "Откатить", first.ID)
	p := h.submit(path, path+"/rollback", url.Values{"deploy": {first.ID}})
	wantStatus(t, p, http.StatusSeeOther)
	after := h.follow(p)
	wantContains(t, after.body, "снова опубликован релиз "+first.ID)
	if got, _ := h.env.Served(v.Site.ID+".pp.test", "index.html"); got != "one" {
		t.Fatalf("served %q after rollback", got)
	}
	cur := strings.Index(after.body, "release--current")
	if cur < 0 || !strings.Contains(after.body[cur:cur+200], first.ID) {
		t.Fatal("history does not mark the rolled-back release as current")
	}

	missing := h.follow(h.submit(path, path+"/rollback", url.Values{"deploy": {"20260101T000000-zzzzzz"}}))
	wantContains(t, missing.body, "Страница не найдена")
}
