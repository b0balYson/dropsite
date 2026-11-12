package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestDeployQualityReport(t *testing.T) {
	h := newHarness(t)
	_, owner := h.user("owner@pp.test")
	_, other := h.user("other@pp.test")
	site := h.createSite(owner, "Landing", "")

	rec := h.deploy(site.ID, owner, testenv.Zip(t, index(`<html><body><a href="/abuot">x</a></body></html>`)))
	wantStatus(t, rec, http.StatusCreated)
	res := decodeAs[deployResultDTO](t, rec)
	if res.Quality == nil || res.Quality.Grade == "" || len(res.Quality.Issues) == 0 ||
		!strings.Contains(strings.Join(res.Quality.Issues, "\n"), "broken link") {
		t.Fatalf("summary %+v", res.Quality)
	}

	url := "/api/v1/sites/" + site.ID + "/deploys/" + res.DeployID
	rec = h.do(http.MethodGet, url, owner, nil)
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[deployReportDTO](t, rec)
	if got.ID != res.DeployID || !got.Current || got.Quality == nil || got.Quality.Counts.BrokenLinks != 1 ||
		got.Quality.PagesMissing.Title[0] != "index.html" {
		t.Fatalf("report %+v", got)
	}
	rec = h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/deploys/current", owner, nil)
	wantStatus(t, rec, http.StatusOK)
	if cur := decodeAs[deployReportDTO](t, rec); cur.ID != res.DeployID {
		t.Fatalf("current %+v", cur)
	}
	wantError(t, h.do(http.MethodGet, url, other, nil), http.StatusNotFound, "not_found")
	wantError(t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/deploys/nope", owner, nil), http.StatusNotFound, "not_found")

	// Rollback results carry the target's summary.
	rec = h.deploy(site.ID, owner, testenv.Zip(t, index(`<p>v2</p>`)))
	wantStatus(t, rec, http.StatusCreated)
	rec = h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/rollback", owner, map[string]string{"deploy_id": res.DeployID})
	wantStatus(t, rec, http.StatusOK)
	if rb := decodeAs[deployResultDTO](t, rec); rb.Quality == nil || rb.Quality.Score != res.Quality.Score {
		t.Fatalf("rollback summary %+v", rb.Quality)
	}
}
