package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func previewOf(siteID, deployID string) string {
	return "https://p-" + deployID + "-" + siteID + ".pp.test/"
}

func TestPreviewURLInWildcardMode(t *testing.T) {
	h := newHarness(t, testenv.WithEnv("TLS_MODE", "wildcard"), testenv.WithEnv("KEEP_RELEASES", "2"))
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")

	var results []deployResultDTO
	for _, body := range []string{"v1", "v2", "v3"} {
		rec := h.deploy(site.ID, tok, testenv.Zip(t, index(body)))
		wantStatus(t, rec, http.StatusCreated)
		res := decodeAs[deployResultDTO](t, rec)
		want := previewOf(site.ID, res.DeployID)
		if res.Deploy.PreviewURL == nil || *res.Deploy.PreviewURL != want {
			t.Fatalf("deploy preview_url %v, want %s", res.Deploy.PreviewURL, want)
		}
		if res.Site.CurrentDeploy == nil || res.Site.CurrentDeploy.PreviewURL == nil || *res.Site.CurrentDeploy.PreviewURL != want {
			t.Fatalf("site current_deploy.preview_url %+v", res.Site.CurrentDeploy)
		}
		results = append(results, res)
		h.env.Clock.Advance(time.Second)
	}
	if got, err := h.env.Served("p-"+results[1].DeployID+"-"+site.ID+".pp.test", "index.html"); err != nil || got != "v2" {
		t.Fatalf("preview serves %q, %v", got, err)
	}

	ds := decodeAs[list[deployDTO]](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/deploys", tok, nil))
	for _, d := range ds.Items {
		switch d.Status {
		case "ready":
			if d.PreviewURL == nil || *d.PreviewURL != previewOf(site.ID, d.ID) {
				t.Fatalf("ready %s: preview_url %v", d.ID, d.PreviewURL)
			}
		default:
			if d.PreviewURL != nil {
				t.Fatalf("%s %s: preview_url %v, want null", d.Status, d.ID, *d.PreviewURL)
			}
		}
	}
	if ds.Items[len(ds.Items)-1].Status != "pruned" {
		t.Fatalf("oldest release should be pruned: %+v", ds.Items)
	}

	rec := h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/rollback", tok, map[string]string{"deploy_id": results[1].DeployID})
	wantStatus(t, rec, http.StatusOK)
	rb := decodeAs[deployResultDTO](t, rec)
	if rb.Deploy.PreviewURL == nil || *rb.Deploy.PreviewURL != previewOf(site.ID, results[1].DeployID) {
		t.Fatalf("rollback preview_url %v", rb.Deploy.PreviewURL)
	}
}

func TestPreviewURLIsNullInOnDemandMode(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")
	rec := h.deploy(site.ID, tok, testenv.Zip(t, index("v1")))
	wantStatus(t, rec, http.StatusCreated)
	if res := decodeAs[deployResultDTO](t, rec); res.Deploy.PreviewURL != nil {
		t.Fatalf("preview_url %v", *res.Deploy.PreviewURL)
	}
	// The field is present and null, not omitted.
	raw := h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/deploys", tok, nil).Body.String()
	if !strings.Contains(raw, `"preview_url":null`) {
		t.Fatalf("body %s", raw)
	}
}
