package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

// TestForeignResourcesLookMissing checks that every endpoint addressing a
// resource answers a stranger exactly as it answers for a resource that does
// not exist.
func TestForeignResourcesLookMissing(t *testing.T) {
	h := newHarness(t)
	_, owner := h.user("owner@pp.test")
	_, stranger := h.user("stranger@pp.test")
	site := h.createSite(owner, "Private", "private-slug")
	wantStatus(t, h.deploy(site.ID, owner, testenv.Zip(t, index("v1"))), http.StatusCreated)
	h.env.Clock.Advance(1e9)
	second := decodeAs[deployResultDTO](t, h.deploy(site.ID, owner, testenv.Zip(t, index("v2"))))
	ownerTokens := decodeAs[list[tokenDTO]](t, h.do(http.MethodGet, "/api/v1/tokens", owner, nil))
	foreignToken := ownerTokens.Items[0].ID

	archive, ctype := multipartBody(t, "archive", testenv.Zip(t, index("hijack")))
	type call struct {
		method, path string
		body         []byte
		ctype        string
	}
	calls := func(ref, tokenID string) []call {
		return []call{
			{http.MethodGet, "/api/v1/sites/" + ref, nil, ""},
			{http.MethodPatch, "/api/v1/sites/" + ref, []byte(`{"title":"mine now"}`), "application/json"},
			{http.MethodDelete, "/api/v1/sites/" + ref, nil, ""},
			{http.MethodPost, "/api/v1/sites/" + ref + "/deploys", archive, ctype},
			{http.MethodGet, "/api/v1/sites/" + ref + "/deploys", nil, ""},
			{http.MethodPost, "/api/v1/sites/" + ref + "/rollback", nil, ""},
			{http.MethodPost, "/api/v1/sites/" + ref + "/rollback", []byte(`{"deploy_id":"` + second.DeployID + `"}`), "application/json"},
			{http.MethodDelete, "/api/v1/tokens/" + tokenID, nil, ""},
			{http.MethodGet, "/api/v1/admin/users", nil, ""},
			{http.MethodPost, "/api/v1/admin/users", []byte(`{"email":"x@pp.test"}`), "application/json"},
			{http.MethodPatch, "/api/v1/admin/users/1", []byte(`{"blocked":true}`), "application/json"},
			{http.MethodGet, "/api/v1/admin/sites", nil, ""},
			{http.MethodPost, "/api/v1/admin/sites/" + ref + "/apex", nil, ""},
			{http.MethodDelete, "/api/v1/admin/sites/" + ref + "/apex", nil, ""},
			{http.MethodPost, "/api/v1/admin/sites/" + ref + "/transfer", []byte(`{"user_id":"1"}`), "application/json"},
			{http.MethodGet, "/api/v1/admin/audit", nil, ""},
		}
	}
	send := func(c call) *httptest.ResponseRecorder {
		var opts []reqOpt
		if c.ctype != "" {
			opts = append(opts, withHeader("Content-Type", c.ctype))
		}
		var body any
		if c.body != nil {
			body = bytes.NewReader(c.body)
		}
		return h.do(c.method, c.path, stranger, body, opts...)
	}
	for _, ref := range []string{site.ID, "private-slug"} {
		foreign := calls(ref, foreignToken)
		missing := calls("zzzzzzzzzz", "987654")
		for i := range foreign {
			name := foreign[i].method + " " + foreign[i].path
			t.Run(name, func(t *testing.T) {
				got, want := send(foreign[i]), send(missing[i])
				if got.Code != http.StatusNotFound || got.Body.String() != want.Body.String() || got.Body.String() != notFoundBody {
					t.Fatalf("foreign %d %s; missing %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
				}
			})
		}
	}

	// The owner's site and token are untouched.
	s := decodeAs[siteDTO](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID, owner, nil))
	if s.Title != "Private" || s.CurrentDeploy == nil || s.CurrentDeploy.ID != second.DeployID {
		t.Fatalf("site changed: %+v", s)
	}
	if got, _ := h.env.Served("private-slug.pp.test", "index.html"); got != "v2" {
		t.Fatalf("served %q", got)
	}
	wantStatus(t, h.do(http.MethodGet, "/api/v1/me", owner, nil), http.StatusOK)
	if strings.Contains(h.logs.String(), `"level":"ERROR"`) {
		t.Fatalf("unexpected error logs: %s", h.logs.String())
	}
}
