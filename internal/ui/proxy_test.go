package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestProxyScreen(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Bot", "")
	base := "/sites/" + v.Site.ID + "/proxy"

	site := h.get("/sites/" + v.Site.ID)
	wantContains(t, site.body, "Ключи API", base)

	empty := h.get(base)
	wantStatus(t, empty, http.StatusOK)
	wantContains(t, empty.body, "Эндпоинтов пока нет", "любой посетитель")

	bad := h.submit(base, base, url.Values{"name": {"ai"}, "upstream_url": {"https://10.0.0.1/v1"}, "auth": {"bearer"},
		"methods": {"POST"}, "enabled": {"1"}})
	wantStatus(t, bad, http.StatusUnprocessableEntity)
	wantContains(t, bad.body, "Эндпоинт не сохранён", "https://10.0.0.1/v1")

	p := h.submit(base, base, url.Values{"name": {"ai"}, "upstream_url": {"https://api.openai.com/v1"}, "auth": {"bearer"},
		"methods": {"POST"}, "paths": {"/chat/completions\n/models"}, "enabled": {"1"}})
	wantStatus(t, p, http.StatusSeeOther)
	list := h.follow(p)
	wantContains(t, list.body, `id="proxy-ai"`, "Нет ключа", "/_dropsite/proxy/ai/", "/chat/completions, /models", `type="password"`)

	const key = "sk-ui-secret-777"
	p = h.submit(base, base+"/ai/secret", url.Values{"secret": {key}})
	wantStatus(t, p, http.StatusSeeOther)
	shown := h.follow(p)
	wantContains(t, shown.body, "Ключ задан", "Ключ эндпоинта ai сохранён")
	wantNotContains(t, shown.body, key)
	edit := h.get(base + "/ai/edit")
	wantStatus(t, edit, http.StatusOK)
	wantContains(t, edit.body, "Эндпоинт ai", "https://api.openai.com/v1")
	wantNotContains(t, edit.body, key)
	views, err := h.env.Svc.ListProxyEndpoints(context.Background(), alice, v.Site.ID)
	if err != nil || !views[0].SecretSet {
		t.Fatalf("secret not stored: %+v %v", views, err)
	}

	// Another user cannot see or change the endpoint.
	h.submit(base, "/logout", nil)
	h.loginUser("bob@pp.test")
	wantStatus(t, h.get(base), http.StatusNotFound)
	wantStatus(t, h.submit("/sites", base+"/ai/secret", url.Values{"secret": {"x"}}), http.StatusNotFound)
	h.submit("/sites", "/logout", nil)
	h.login("alice@pp.test", testenv.UserPassword)

	p = h.submit(base, base+"/ai/secret/delete", nil)
	wantContains(t, h.follow(p).body, "Нет ключа")
	p = h.submit(base, base+"/ai/delete", nil)
	wantContains(t, h.follow(p).body, "Эндпоинт ai удалён", "Эндпоинтов пока нет")
}
