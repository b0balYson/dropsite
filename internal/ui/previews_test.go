package ui

import (
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestHistoryShowsPreviewLinksInWildcardMode(t *testing.T) {
	h := newHarness(t, testenv.WithEnv("TLS_MODE", "wildcard"))
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	first := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	h.env.Clock.Advance(2e9)
	second := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "two"})

	for _, path := range []string{"/sites/" + v.Site.ID, "/sites/" + v.Site.ID + "/history"} {
		p := h.get(path, "HX-Request", "true")
		for _, id := range []string{first.ID, second.ID} {
			href := `href="https://p-` + id + "-" + v.Site.ID + `.pp.test/"`
			wantContains(t, p.body, href, `target="_blank" rel="noopener noreferrer"`)
		}
	}
	wantContains(t, h.get("/sites/"+v.Site.ID).body, "превью")
}

func TestHistoryHasNoPreviewLinksInOnDemandMode(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	d := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	body := h.get("/sites/" + v.Site.ID).body
	if strings.Contains(body, "https://p-"+d.ID) || strings.Contains(body, "release__preview") {
		t.Fatal("ondemand mode must not show preview links")
	}
}
