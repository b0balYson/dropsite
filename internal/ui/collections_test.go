package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestCollectionsPages(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Cafe", "")
	base := "/sites/" + v.Site.ID + "/data"

	site := h.get(base)
	wantContains(t, site.body, "Данные", "Коллекций пока нет", `href="`+base+`" aria-current="page"`)

	// Errors keep the typed values.
	p := h.submit(base, base, url.Values{"name": {"reviews"}, "mode": {"moderated"}, "title": {"Отзывы"}})
	wantStatus(t, p, http.StatusUnprocessableEntity)
	wantContains(t, p.body, "Проверьте настройки коллекции", `value="reviews"`, "Отзывы")

	p = h.submit(base, base, url.Values{"name": {"menu"}, "mode": {"public"}, "title": {"Меню"}})
	wantStatus(t, p, http.StatusSeeOther)
	coll := base + "/menu"
	if p.location() != coll {
		t.Fatalf("location %q", p.location())
	}
	page := h.get(coll)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page.body, "Меню", "fetch(&#39;/_dropsite/data/menu&#39;)", "Добавить документ")

	// A broken JSON document is not saved and the text stays in the field.
	p = h.submit(coll, coll+"/documents", url.Values{"id": {"tea"}, "data": {`{"price": 1,`}})
	wantStatus(t, p, http.StatusUnprocessableEntity)
	wantContains(t, p.body, "Документ не сохранён", "{&#34;price&#34;: 1,", `aria-invalid="true"`)
	p = h.submit(coll, coll+"/documents", url.Values{"id": {"tea"}, "position": {"2"}, "data": {`{"price": "<b>1</b>"}`}})
	wantStatus(t, p, http.StatusSeeOther)
	page = h.get(coll)
	wantContains(t, page.body, "tea", "&lt;b&gt;1&lt;/b&gt;")
	wantNotContains(t, page.body, "<b>1</b>")
	edit := h.get(coll + "?edit=tea")
	wantContains(t, edit.body, "Изменить tea", "readonly")

	// Settings: a schema error is shown next to the collection.
	p = h.submit(base, coll, url.Values{"title": {"Меню"}, "mode": {"public"}, "schema": {`{"type":"array"}`}})
	wantStatus(t, p, http.StatusUnprocessableEntity)
	wantContains(t, p.body, "type")
	p = h.submit(base, coll, url.Values{"title": {"Меню"}, "mode": {"public"},
		"schema": {`{"type":"object","properties":{"price":{"type":"number"}}}`}})
	wantStatus(t, p, http.StatusSeeOther)
	p = h.submit(coll, coll+"/documents", url.Values{"id": {"cake"}, "data": {`{"price": "x"}`}})
	wantStatus(t, p, http.StatusUnprocessableEntity)
	wantContains(t, p.body, "price")

	p = h.submit(base, coll+"/delete", url.Values{"confirm": {"men"}})
	wantStatus(t, p, http.StatusSeeOther)
	if _, err := h.env.Svc.GetCollection(context.Background(), alice, v.Site.ID, "menu"); err != nil {
		t.Fatal("deleted without confirmation")
	}
	p = h.submit(base, coll+"/delete", url.Values{"confirm": {"menu"}})
	wantStatus(t, p, http.StatusSeeOther)
	wantStatus(t, h.get(coll), http.StatusNotFound)
}

func TestCollectionModerationPage(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Cafe", "")
	ctx := context.Background()
	schema := []byte(`{"type":"object","properties":{"text":{"type":"string"}}}`)
	if _, err := h.env.Svc.CreateCollection(ctx, alice, v.Site.ID, service.CollectionInput{Name: "reviews",
		Mode: "moderated", Schema: schema}); err != nil {
		t.Fatal(err)
	}
	host := v.Site.ID + "." + testenv.BaseDomain
	for i, text := range []string{"one", "two", "three"} {
		if _, err := h.env.Svc.SubmitDocument(ctx, service.DataPost{Host: host, Collection: "reviews",
			IP: "198.51.100." + string(rune('1'+i)), Body: []byte(`{"text":"` + text + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	coll := "/sites/" + v.Site.ID + "/data/reviews"
	page := h.get(coll + "?status=pending")
	wantContains(t, page.body, "Одобрить отмеченные", "ждут: 3")
	pending, err := h.env.Svc.ListDocuments(ctx, alice, v.Site.ID, "reviews", service.DocumentQuery{Status: "pending"})
	if err != nil || len(pending.Items) != 3 {
		t.Fatalf("pending %v %v", pending, err)
	}
	p := h.submit(coll, coll+"/moderate", url.Values{"action": {"approve"},
		"ids": {pending.Items[0].ID, pending.Items[1].ID}})
	wantStatus(t, p, http.StatusSeeOther)
	wantContains(t, h.follow(p).body, "Одобрено: 2.")
	got, _ := h.env.Svc.ReadCollection(ctx, service.DataRead{Host: host, Collection: "reviews"})
	if len(got.Items) != 2 {
		t.Fatalf("public %d", len(got.Items))
	}
	p = h.submit(coll, coll+"/moderate", url.Values{"action": {"reject"}, "ids": {pending.Items[2].ID}})
	wantContains(t, h.follow(p).body, "Отклонено: 1.")

	// Another user's collections do not exist.
	bob := h.env.User("bob@pp.test")
	bv := h.createSite(bob, "Bob", "")
	wantStatus(t, h.get("/sites/"+bv.Site.ID+"/data"), http.StatusNotFound)
	wantStatus(t, h.submit(coll, "/sites/"+bv.Site.ID+"/data", url.Values{"name": {"x"}}), http.StatusNotFound)
}
