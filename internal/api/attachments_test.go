package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestSiteAttachmentsEndpoint(t *testing.T) {
	f := newFormHarness(t)
	path := "/api/v1/sites/" + f.site.ID + "/attachments"

	rec := f.do(http.MethodGet, path, f.owner, nil)
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[siteAttachmentsDTO](t, rec)
	if got.SiteID != f.site.ID || got.Forms.Names == nil || got.Forms.Channels != 0 {
		t.Fatalf("empty: %s", rec.Body.String())
	}

	base := "/api/v1/sites/" + f.site.ID + "/forms/channels"
	wantStatus(t, f.do(http.MethodPost, base, f.owner, map[string]string{"kind": "webhook", "target": "https://hooks.example.com/a"}), http.StatusCreated)
	rec = f.do(http.MethodPost, base, f.owner, map[string]string{"kind": "webhook", "target": "https://hooks.example.com/b"})
	off := decodeAs[channelResultDTO](t, rec)
	wantStatus(t, f.do(http.MethodPatch, base+"/"+off.Channel.ID, f.owner, map[string]bool{"enabled": false}), http.StatusOK)

	got = decodeAs[siteAttachmentsDTO](t, f.do(http.MethodGet, path, f.owner, nil))
	if got.Forms.Channels != 2 || got.Forms.WorkingChannels != 1 {
		t.Fatalf("channels: %+v", got)
	}

	_, stranger := f.user("stranger@pp.test")
	wantError(t, f.do(http.MethodGet, path, stranger, nil), http.StatusNotFound, "not_found")
}

func TestDeployWithSecretAnswersSecretsFound(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("u@pp.test")
	site := h.createSite(tok, "Shop", "")
	key := "gh" + "p_" + strings.Repeat("aB3dE5fG7hJ9kL2mN4pQ", 2)[:36]
	rec := h.deploy(site.ID, tok, testenv.Zip(t, map[string]string{"index.html": "x", "config.js": key}))
	e := wantError(t, rec, http.StatusBadRequest, "secrets_found")
	if !strings.Contains(e.Details["found"].(string), "config.js:1 (GitHub token)") || e.Details["count"] != float64(1) ||
		strings.Contains(rec.Body.String(), key) {
		t.Fatalf("details: %s", rec.Body.String())
	}
}
