package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/testenv"
)

// deployQuery publishes an archive with a query string.
func (h *harness) deployQuery(ref, query, token string, archive []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	body, ctype := multipartBody(h.t, "archive", archive)
	return h.do(http.MethodPost, "/api/v1/sites/"+ref+"/deploys?"+query, token, bytes.NewReader(body),
		withHeader("Content-Type", ctype))
}

func TestDraftsThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")
	wantStatus(t, h.deploy(site.ID, tok, testenv.Zip(t, index("live"))), http.StatusCreated)
	h.env.Clock.Advance(time.Second)

	rec := h.deployQuery(site.ID, "draft=true", tok, testenv.Zip(t, index("draft")))
	wantStatus(t, rec, http.StatusCreated)
	res := decodeAs[deployResultDTO](t, rec)
	if !res.Deploy.Draft || res.Site.Draft == nil || res.Site.Draft.DeployID != res.DeployID {
		t.Fatalf("draft result %+v", res)
	}
	want := "https://p-" + site.ID + ".pp.test/"
	if res.Site.Draft.PreviewURL == nil || *res.Site.Draft.PreviewURL != want {
		t.Fatalf("preview_url %v, want %s", res.Site.Draft.PreviewURL, want)
	}
	if res.Site.CurrentDeploy == nil || res.Site.CurrentDeploy.ID == res.DeployID {
		t.Fatal("the draft went live")
	}
	if got, err := h.env.Served("p-"+site.ID+".pp.test", "index.html"); err != nil || got != "draft" {
		t.Fatalf("preview serves %q %v", got, err)
	}
	wantStatus(t, h.deployQuery(site.ID, "draft=maybe", tok, testenv.Zip(t, index("x"))), http.StatusBadRequest)

	ds := decodeAs[list[deployDTO]](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/deploys", tok, nil))
	drafts := 0
	for _, d := range ds.Items {
		if d.Draft {
			drafts++
			if d.ID != res.DeployID {
				t.Fatalf("wrong release marked as draft: %s", d.ID)
			}
		}
	}
	if drafts != 1 {
		t.Fatalf("%d releases marked as draft", drafts)
	}

	// Strangers get 404.
	_, other := h.user("eve@pp.test")
	wantStatus(t, h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/draft/publish", other, nil), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/sites/"+site.ID+"/draft", other, nil), http.StatusNotFound)

	rec = h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/draft/publish", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	pub := decodeAs[deployResultDTO](t, rec)
	if pub.DeployID != res.DeployID || pub.Site.Draft != nil || pub.Site.CurrentDeploy.ID != res.DeployID {
		t.Fatalf("publish %+v", pub)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/draft/publish", tok, nil), http.StatusBadRequest, "no_draft")

	// A draft from a file edit, based on the current release, then discarded.
	h.env.Clock.Advance(time.Second)
	rec = h.do(http.MethodPatch, "/api/v1/sites/"+site.ID+"/files", tok, map[string]any{
		"base_deploy_id": pub.DeployID, "draft": true,
		"files": []map[string]string{{"path": "about.html", "content": "about"}},
	})
	wantStatus(t, rec, http.StatusOK)
	edit := decodeAs[filesResultDTO](t, rec)
	if !edit.Deploy.Draft || edit.Site.CurrentDeploy.ID != pub.DeployID {
		t.Fatalf("edit draft %+v", edit.deployResultDTO)
	}
	rec = h.do(http.MethodDelete, "/api/v1/sites/"+site.ID+"/draft", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	if s := decodeAs[siteDTO](t, rec); s.Draft != nil {
		t.Fatal("draft kept after discard")
	}
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/sites/"+site.ID+"/draft", tok, nil), http.StatusBadRequest)
}

func TestUploadCommitAsDraft(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")
	wantStatus(t, h.deploy(site.ID, tok, testenv.Zip(t, index("live"))), http.StatusCreated)
	h.env.Clock.Advance(time.Second)

	rec := h.do(http.MethodPost, "/api/v1/uploads", tok, map[string]any{"site": site.ID})
	wantStatus(t, rec, http.StatusCreated)
	up := decodeAs[uploadDTO](t, rec)
	rec = h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/files", tok, map[string]any{
		"files": []map[string]string{{"path": "index.html", "content": "big draft"}},
	})
	wantStatus(t, rec, http.StatusOK)
	rec = h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/commit", tok, map[string]any{"draft": true})
	wantStatus(t, rec, http.StatusCreated)
	res := decodeAs[filesResultDTO](t, rec)
	if !res.Deploy.Draft || res.Site.Draft == nil || res.Site.CurrentDeploy.ID == res.DeployID {
		t.Fatalf("upload draft %+v", res.deployResultDTO)
	}
}
