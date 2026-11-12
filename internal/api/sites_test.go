package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestSiteLifecycle(t *testing.T) {
	h := newHarness(t)
	bob, tok := h.user("bob@pp.test")

	site := h.createSite(tok, "Promo", "promo-may")
	if site.Slug == nil || *site.Slug != "promo-may" || site.URL != "https://promo-may.pp.test/" ||
		len(site.URLs) != 2 || len(site.Hostnames) != 2 || site.CurrentDeploy != nil || site.OwnerID != idString(bob.User.ID) {
		t.Fatalf("created %+v", site)
	}

	// By ID and by slug the site is the same.
	byID := decodeAs[siteDTO](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID, tok, nil))
	bySlug := decodeAs[siteDTO](t, h.do(http.MethodGet, "/api/v1/sites/promo-may", tok, nil))
	if byID.ID != site.ID || bySlug.ID != site.ID {
		t.Fatalf("lookup mismatch %s %s", byID.ID, bySlug.ID)
	}

	// Publish v1 and v2.
	first := h.deploy(site.ID, tok, testenv.Zip(t, index("v1")), withHeader("X-Pushpage-Client", "cli/1.2.3"))
	wantStatus(t, first, http.StatusCreated)
	r1 := decodeAs[deployResultDTO](t, first)
	if r1.DeployID == "" || r1.URL != "https://promo-may.pp.test/" || len(r1.URLs) != 2 ||
		r1.Deploy.Source != "cli" || !r1.Deploy.Current || r1.Deploy.FileCount != 1 ||
		r1.Site.CurrentDeploy == nil || r1.Site.CurrentDeploy.ID != r1.DeployID || r1.Deploy.CreatedBy != idString(bob.User.ID) {
		t.Fatalf("deploy result %+v", r1)
	}
	if got, _ := h.env.Served("promo-may.pp.test", "index.html"); got != "v1" {
		t.Fatalf("served %q", got)
	}
	h.env.Clock.Advance(1e9)
	second := decodeAs[deployResultDTO](t, h.deploy("promo-may", tok, testenv.TarGz(t, index("v2"))))
	if second.Deploy.Source != "api" {
		t.Fatalf("source %q", second.Deploy.Source)
	}

	// History, newest first, with the current flag.
	hist := decodeAs[list[deployDTO]](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/deploys", tok, nil))
	if hist.Total != 2 || hist.Items[0].ID != second.DeployID || !hist.Items[0].Current || hist.Items[1].Current {
		t.Fatalf("history %+v", hist)
	}

	// Rollback without deploy_id goes to the previous release.
	rec := h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/rollback", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	rb := decodeAs[deployResultDTO](t, rec)
	if rb.DeployID != r1.DeployID || !rb.Deploy.Current || rb.Site.CurrentDeploy.ID != r1.DeployID {
		t.Fatalf("rollback %+v", rb)
	}
	if got, _ := h.env.Served("promo-may.pp.test", "index.html"); got != "v1" {
		t.Fatalf("served after rollback %q", got)
	}
	// Rollback to an explicit release.
	rec = h.do(http.MethodPost, "/api/v1/sites/promo-may/rollback", tok, map[string]string{"deploy_id": second.DeployID})
	wantStatus(t, rec, http.StatusOK)
	if got, _ := h.env.Served("promo-may.pp.test", "index.html"); got != "v2" {
		t.Fatalf("served after explicit rollback %q", got)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/sites/promo-may/rollback", tok, map[string]string{"deploy_id": "nope"}),
		http.StatusNotFound, domain.CodeNotFound)

	// Delete, then everything is 404.
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/sites/"+site.ID, tok, nil), http.StatusNoContent)
	wantError(t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID, tok, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodDelete, "/api/v1/sites/"+site.ID, tok, nil), http.StatusNotFound, domain.CodeNotFound)
}

func TestRollbackWithoutPrevious(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Solo", "")
	wantStatus(t, h.deploy(site.ID, tok, testenv.Zip(t, index("v1"))), http.StatusCreated)
	wantError(t, h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/rollback", tok, "{}"),
		http.StatusBadRequest, domain.CodeNoPrevious)
	wantError(t, h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/rollback", tok, `{"deploy":"x"}`),
		http.StatusBadRequest, domain.CodeInvalidInput)
}

func TestPatchSite(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Promo", "")
	h.createSite(tok, "Other", "taken")
	path := "/api/v1/sites/" + site.ID

	rec := h.do(http.MethodPatch, path, tok, `{"slug":"fresh"}`)
	wantStatus(t, rec, http.StatusOK)
	if s := decodeAs[siteDTO](t, rec); s.Slug == nil || *s.Slug != "fresh" || s.Title != "Promo" {
		t.Fatalf("slug set %+v", s)
	}
	// Missing slug leaves it unchanged.
	rec = h.do(http.MethodPatch, path, tok, `{"title":"Renamed"}`)
	if s := decodeAs[siteDTO](t, rec); s.Slug == nil || *s.Slug != "fresh" || s.Title != "Renamed" {
		t.Fatalf("title only %+v", s)
	}
	// null removes the slug.
	rec = h.do(http.MethodPatch, path, tok, `{"slug":null}`)
	if s := decodeAs[siteDTO](t, rec); s.Slug != nil || len(s.URLs) != 1 {
		t.Fatalf("slug null %+v", s)
	}
	h.do(http.MethodPatch, path, tok, `{"slug":"again"}`)
	// "" removes it too.
	rec = h.do(http.MethodPatch, path, tok, `{"slug":""}`)
	if s := decodeAs[siteDTO](t, rec); s.Slug != nil {
		t.Fatalf("slug empty %+v", s)
	}

	wantError(t, h.do(http.MethodPatch, path, tok, `{"slug":"taken"}`), http.StatusConflict, domain.CodeSlugTaken)
	wantError(t, h.do(http.MethodPatch, path, tok, `{"slug":"admin"}`), http.StatusBadRequest, domain.CodeSlugReserved)
	wantError(t, h.do(http.MethodPatch, path, tok, `{"slug":"Bad_Slug!"}`), http.StatusBadRequest, domain.CodeSlugInvalid)
	wantError(t, h.do(http.MethodPatch, path, tok, `{"title":null}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodPatch, path, tok, `{"title":""}`), http.StatusBadRequest, domain.CodeTitleInvalid)
	wantError(t, h.do(http.MethodPatch, path, tok, `{"slug":5}`), http.StatusBadRequest, domain.CodeInvalidInput)
}

func TestCreateSiteErrors(t *testing.T) {
	h := newHarness(t, testenv.WithEnv("MAX_SITES_PER_USER", "1"))
	_, tok := h.user("bob@pp.test")
	wantError(t, h.do(http.MethodPost, "/api/v1/sites", tok, `{"title":"x","slug":"www"}`), http.StatusBadRequest, domain.CodeSlugReserved)
	h.createSite(tok, "One", "one")
	p := wantError(t, h.do(http.MethodPost, "/api/v1/sites", tok, `{"title":"Two"}`), http.StatusForbidden, domain.CodeSiteLimit)
	if p.Details["limit"] != float64(1) {
		t.Fatalf("details %v", p.Details)
	}
	_, tok2 := h.user("carol@pp.test")
	wantError(t, h.do(http.MethodPost, "/api/v1/sites", tok2, `{"title":"x","slug":"one"}`), http.StatusConflict, domain.CodeSlugTaken)
}

func TestListSitesShowsOnlyOwn(t *testing.T) {
	h := newHarness(t)
	_, bob := h.user("bob@pp.test")
	_, carol := h.user("carol@pp.test")
	s := h.createSite(bob, "Bob site", "bobs")
	h.createSite(carol, "Carol site", "")
	wantStatus(t, h.deploy(s.ID, bob, testenv.Zip(t, index(strings.Repeat("x", 100)))), http.StatusCreated)
	l := decodeAs[list[siteItemDTO]](t, h.do(http.MethodGet, "/api/v1/sites", bob, nil))
	if l.Total != 1 || l.Items[0].ID != s.ID || l.Items[0].OwnerEmail != "bob@pp.test" ||
		l.Items[0].LastDeployAt == nil || l.Items[0].SizeBytes != 100 || l.Items[0].URL != "https://bobs.pp.test/" {
		t.Fatalf("list %+v", l)
	}
}
