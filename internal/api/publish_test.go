package api

import (
	"net/http"
	"testing"
)

func TestPublishSettingsEndpoints(t *testing.T) {
	h := newHarness(t)
	_, owner := h.user("owner@pp.test")
	_, other := h.user("other@pp.test")
	site := h.createSite(owner, "Landing", "")
	url := "/api/v1/sites/" + site.ID + "/publish-settings"

	rec := h.do(http.MethodGet, url, owner, nil)
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[publishSettingsDTO](t, rec)
	if got != (publishSettingsDTO{SiteID: site.ID, Optimize: true, InstallationEnabled: true, Effective: true}) {
		t.Fatalf("defaults %+v", got)
	}

	rec = h.do(http.MethodPatch, url, owner, map[string]any{"optimize": false})
	wantStatus(t, rec, http.StatusOK)
	if got = decodeAs[publishSettingsDTO](t, rec); got.Optimize || got.Effective || !got.InstallationEnabled {
		t.Fatalf("after patch %+v", got)
	}
	rec = h.do(http.MethodGet, url, owner, nil)
	if got = decodeAs[publishSettingsDTO](t, rec); got.Optimize {
		t.Fatal("setting not stored")
	}

	wantError(t, h.do(http.MethodPatch, url, owner, map[string]any{"optimize": "no"}), http.StatusBadRequest, "invalid_input")
	wantError(t, h.do(http.MethodPatch, url, owner, map[string]any{}), http.StatusBadRequest, "invalid_input")
	wantError(t, h.do(http.MethodGet, url, other, nil), http.StatusNotFound, "not_found")
	wantError(t, h.do(http.MethodPatch, url, other, map[string]any{"optimize": true}), http.StatusNotFound, "not_found")
}
