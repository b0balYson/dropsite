package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestSitePublishSettingsForm(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Docs", "docs")
	site := "/sites/" + v.Site.ID
	page := h.get(site + "/settings")
	wantContains(t, page.body, "Оптимизировать при публикации", `name="optimize" value="1" checked`)

	p := h.submit(site, site+"/publish-settings", url.Values{})
	wantStatus(t, p, http.StatusSeeOther)
	if p.location() != site+"/settings#optimize-h" {
		t.Fatalf("redirect %q", p.location())
	}
	got, err := h.env.Svc.GetPublishSettings(context.Background(), alice, v.Site.ID)
	if err != nil || got.Optimize {
		t.Fatalf("after untick %+v %v", got, err)
	}
	page = h.get(site)
	wantContains(t, page.body, "Настройки публикации сохранены")
	wantNotContains(t, page.body, `name="optimize" value="1" checked`)

	h.c = h.newClient()
	h.loginUser("bob@pp.test")
	h.submit("/", site+"/publish-settings", url.Values{"optimize": {"1"}})
	got, _ = h.env.Svc.GetPublishSettings(context.Background(), alice, v.Site.ID)
	if got.Optimize {
		t.Fatal("foreign user changed the setting")
	}
}
