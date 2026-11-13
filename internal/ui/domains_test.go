package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestOwnerDomainsBlock(t *testing.T) {
	h := newHarness(t)
	ann := h.loginUser("ann@pp.test")
	site := h.createSite(ann, "Promo", "")
	path := "/sites/" + site.Site.ID

	p := h.get(path + "/access")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, "Свои домены", "Своих доменов пока нет", site.Site.ID+".<wbr>pp.<wbr>test", testenv.ServerIP, "0 из 5")

	p = h.follow(h.submit(path, path+"/domains", url.Values{"domain": {"Promo.Example.RU"}}))
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, "promo.<wbr>example.<wbr>ru", "Ждёт подтверждения", "добавлен и ждёт подтверждения", "1 из 5")
	wantNotContains(t, p.body, `href="https://promo.example.ru/"`)

	p = h.follow(h.submit(path, path+"/domains", url.Values{"domain": {"Магазин.Пример.РФ"}}))
	wantContains(t, p.body, "xn--80aairftm.<wbr>xn--e1afmkfd.<wbr>xn--p1ai", "магазин.пример.рф")
	p = h.follow(h.submit(path, path+"/domains/remove", url.Values{"domain": {"xn--80aairftm.xn--e1afmkfd.xn--p1ai"}}))
	wantContains(t, p.body, "удалён")

	p = h.follow(h.submit(path, path+"/domains", url.Values{"domain": {"203.0.113.7"}}))
	wantContains(t, p.body, "не похоже на домен")
	p = h.follow(h.submit(path, path+"/domains", url.Values{"domain": {"x.pp.test"}}))
	wantContains(t, p.body, "принадлежит самой платформе")

	// Another user cannot add or remove domains of this site.
	other := h.newClient()
	mine := h.c
	h.c = other
	h.loginUser("bob@pp.test")
	tok := h.token("/")
	wantStatus(t, h.post(path+"/domains", url.Values{csrfField: {tok}, "domain": {"steal.example.ru"}}), http.StatusNotFound)
	wantStatus(t, h.post(path+"/domains/remove", url.Values{csrfField: {tok}, "domain": {"promo.example.ru"}}), http.StatusNotFound)
	h.c = mine

	p = h.follow(h.submit(path, path+"/domains/remove", url.Values{"domain": {"promo.example.ru"}}))
	wantContains(t, p.body, "Домен promo.example.ru удалён", "Своих доменов пока нет")
}

func TestAdminDomainsPage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ann := h.env.User("ann@pp.test")
	site := h.createSite(ann, "Promo", "")
	for _, d := range []string{"promo.example.ru", "bad.example.ru"} {
		if _, err := h.env.Svc.AddDomain(ctx, ann, site.Site.ID, d); err != nil {
			t.Fatal(err)
		}
	}
	h.env.DNS.Set("promo.example.ru", testenv.ServerIP)
	h.env.DNS.Set("bad.example.ru", "198.51.100.1")
	h.loginAdmin()

	p := h.get("/admin/domains")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, "Внешние домены", "2 ждут подтверждения", "ann@<wbr>pp.<wbr>test", "Promo",
		`hx-get="/admin/domains/promo.example.ru/dns"`, "Подтвердить", "Отклонить")

	dns := h.get("/admin/domains/promo.example.ru/dns", "HX-Request", "true")
	wantStatus(t, dns, http.StatusOK)
	wantContains(t, dns.body, "Указывает на сервер", testenv.ServerIP)
	dns = h.get("/admin/domains/bad.example.ru/dns", "HX-Request", "true")
	wantContains(t, dns.body, "Указывает не туда", "198.51.100.1")
	wantStatus(t, h.get("/admin/domains/unknown.example.ru/dns"), http.StatusNotFound)

	p = h.follow(h.submit("/admin/domains", "/admin/domains/promo.example.ru/approve", url.Values{"back": {"/admin/domains"}}))
	wantContains(t, p.body, "Домен promo.example.ru подтверждён", "1 ждут подтверждения")
	if got, _ := h.env.Served("promo.example.ru", "index.html"); got != "" {
		t.Fatalf("empty site serves %q", got)
	}

	p = h.follow(h.submit("/admin/domains", "/admin/domains/bad.example.ru/reject", url.Values{"reason": {""}}))
	wantContains(t, p.body, "Укажите причину")
	p = h.follow(h.submit("/admin/domains", "/admin/domains/bad.example.ru/reject",
		url.Values{"reason": {"<script>alert(1)</script>"}, "back": {"/admin/domains?status=rejected"}}))
	wantContains(t, p.body, "Домен bad.example.ru отклонён", "&lt;script&gt;alert(1)&lt;/script&gt;")
	wantNotContains(t, p.body, "<script>alert(1)</script>")

	active := h.get("/admin/domains?status=active")
	wantContains(t, active.body, "promo.<wbr>example.<wbr>ru", "Снять подтверждение")
	wantNotContains(t, active.body, "bad.<wbr>example.<wbr>ru")
	wantStatus(t, h.get("/admin/domains?status=weird"), http.StatusOK)

	// The owner sees the decision and the reason.
	h.c = h.newClient()
	h.login("ann@pp.test", testenv.UserPassword)
	own := h.get("/sites/" + site.Site.ID + "/access")
	wantContains(t, own.body, "Подтверждён", "Отклонён", "&lt;script&gt;alert(1)&lt;/script&gt;", `href="https://promo.example.ru/"`)
	wantStatus(t, h.get("/admin/domains"), http.StatusNotFound)
	wantStatus(t, h.get("/admin/domains/promo.example.ru/dns"), http.StatusNotFound)
	tok := h.token("/")
	wantStatus(t, h.post("/admin/domains/bad.example.ru/approve", url.Values{csrfField: {tok}}), http.StatusNotFound)
}

func TestAdminDomainsBackIsSameSite(t *testing.T) {
	h := newHarness(t)
	ann := h.env.User("ann@pp.test")
	site := h.createSite(ann, "Promo", "")
	if _, err := h.env.Svc.AddDomain(context.Background(), ann, site.Site.ID, "promo.example.ru"); err != nil {
		t.Fatal(err)
	}
	h.loginAdmin()
	p := h.submit("/admin/domains", "/admin/domains/promo.example.ru/approve", url.Values{"back": {"https://evil.example/"}})
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != "/admin/domains" {
		t.Fatalf("redirect to %q", p.location())
	}
}
