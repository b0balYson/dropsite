package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestCreateCollectionFromTemplate(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Cafe", "")
	base := "/sites/" + v.Site.ID + "/data"

	page := h.get(base)
	wantContains(t, page.body, "Начать с шаблона", `<option value="reviews"`, "Отзывы (с модерацией)",
		`name="with_samples" value="1" checked`)

	// The mode radio says public, but the reviews template is moderated.
	p := h.submit(base, base, url.Values{"name": {"reviews"}, "mode": {"public"}, "template": {"reviews"},
		"with_samples": {"1"}})
	wantStatus(t, p, http.StatusSeeOther)
	c, err := h.env.Svc.GetCollection(context.Background(), alice, v.Site.ID, "reviews")
	if err != nil || c.Mode != domain.CollectionModerated || c.Documents != 2 {
		t.Fatalf("collection %+v %v", c.Collection, err)
	}
	coll := h.get(base + "/reviews")
	wantContains(t, coll.body, "altcha-widget", "Anna", "&lt;!-- Fallback content")

	// Without a template the samples box is ignored; an unknown template keeps the form.
	p = h.submit(base, base, url.Values{"name": {"plain"}, "mode": {"public"}, "with_samples": {"1"}})
	wantStatus(t, p, http.StatusSeeOther)
	p = h.submit(base, base, url.Values{"name": {"blog"}, "mode": {"public"}, "template": {"blog"}})
	wantStatus(t, p, http.StatusUnprocessableEntity)
	wantContains(t, p.body, `value="blog"`, "unknown template")
}
