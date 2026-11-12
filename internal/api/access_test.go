package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSiteAccessFields(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	_, eve := h.user("eve@pp.test")
	site := h.createSite(tok, "Docs", "docs")
	if site.Noindex || site.Protected || site.ExpiresAt != nil {
		t.Fatalf("new site %+v", site)
	}
	path := "/api/v1/sites/" + site.ID

	rec := h.do(http.MethodPatch, path, tok, `{"protected": true, "password": "visitor-pass-1", "noindex": true}`)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "visitor-pass-1") || strings.Contains(rec.Body.String(), "argon2") {
		t.Fatalf("password leaked: %s", rec.Body)
	}
	got := decodeAs[siteDTO](t, rec)
	if !got.Protected || !got.Noindex {
		t.Fatalf("after patch %+v", got)
	}

	exp := h.env.Clock.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	got = decodeAs[siteDTO](t, h.do(http.MethodPatch, path, tok, map[string]any{"expires_at": exp.Format(time.RFC3339)}))
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("expires_at %v", got.ExpiresAt)
	}
	// The list carries the same fields.
	list := decodeAs[struct {
		Items []siteItemDTO `json:"items"`
		Total int           `json:"total"`
	}](t, h.do(http.MethodGet, "/api/v1/sites", tok, nil))
	if len(list.Items) != 1 || !list.Items[0].Protected || !list.Items[0].Noindex || list.Items[0].ExpiresAt == nil {
		t.Fatalf("list %+v", list.Items)
	}
	got = decodeAs[siteDTO](t, h.do(http.MethodPatch, path, tok, `{"expires_at": null, "protected": false, "noindex": false}`))
	if got.ExpiresAt != nil || got.Protected || got.Noindex {
		t.Fatalf("cleared %+v", got)
	}

	for body, code := range map[string]string{
		`{"protected": true}`:                    domain.CodeSitePasswordRequired,
		`{"password": "short"}`:                  domain.CodePasswordShort,
		`{"protected": false, "password": "x"}`:  domain.CodeInvalidInput,
		`{"noindex": "yes"}`:                     domain.CodeInvalidInput,
		`{"protected": null}`:                    domain.CodeInvalidInput,
		`{"password": null}`:                     domain.CodeInvalidInput,
		`{"expires_at": "tomorrow"}`:             domain.CodeInvalidInput,
		`{"expires_at": "2020-01-01T00:00:00Z"}`: domain.CodeExpiryInvalid,
		`{"expires_at": "2999-01-01T00:00:00Z"}`: domain.CodeExpiryInvalid,
	} {
		wantError(t, h.do(http.MethodPatch, path, tok, body), http.StatusBadRequest, code)
	}
	// Another user's site: 404, nothing changes.
	wantError(t, h.do(http.MethodPatch, path, eve, `{"noindex": true}`), http.StatusNotFound, domain.CodeNotFound)
	if got := decodeAs[siteDTO](t, h.do(http.MethodGet, path, tok, nil)); got.Noindex {
		t.Fatal("foreign patch applied")
	}
}

func TestAdminUserLimits(t *testing.T) {
	h := newHarness(t)
	bob, tok := h.user("bob@pp.test")
	path := "/api/v1/admin/users/" + idString(bob.User.ID)

	// Users never see the endpoint.
	wantError(t, h.do(http.MethodGet, path, tok, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodPatch, path, tok, `{"limits": {"max_files": 5}}`), http.StatusNotFound, domain.CodeNotFound)

	detail := decodeAs[userDetailDTO](t, h.do(http.MethodGet, path, h.adminToken, nil))
	if detail.User.Email != "bob@pp.test" || detail.LimitOverrides.MaxSitesPerUser != nil || detail.Limits.MaxSitesPerUser == 20 {
		t.Fatalf("detail %+v", detail)
	}
	rec := h.do(http.MethodPatch, path, h.adminToken, `{"limits": {"max_sites_per_user": 20, "max_files": null}}`)
	wantStatus(t, rec, http.StatusOK)
	upd := decodeAs[inviteDTO](t, rec)
	if upd.Limits == nil || upd.Limits.MaxSitesPerUser != 20 || upd.LimitOverrides == nil ||
		upd.LimitOverrides.MaxSitesPerUser == nil || *upd.LimitOverrides.MaxSitesPerUser != 20 || upd.InviteURL != "" {
		t.Fatalf("update %+v", upd)
	}
	me := decodeAs[meDTO](t, h.do(http.MethodGet, "/api/v1/me", tok, nil))
	if me.Limits.MaxSitesPerUser != 20 {
		t.Fatalf("me limits %+v", me.Limits)
	}

	wantError(t, h.do(http.MethodPatch, path, h.adminToken, `{"limits": {"keep_releases": 1}}`), http.StatusBadRequest, domain.CodeSettingInvalid)
	wantError(t, h.do(http.MethodPatch, path, h.adminToken, `{"limits": {"bogus": 1}}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodPatch, path, h.adminToken, `{"limits": {}, "blocked": true}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodGet, "/api/v1/admin/users/999", h.adminToken, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodGet, "/api/v1/admin/users/abc", h.adminToken, nil), http.StatusNotFound, domain.CodeNotFound)

	// An empty object resets every override.
	upd = decodeAs[inviteDTO](t, h.do(http.MethodPatch, path, h.adminToken, `{"limits": {}}`))
	if upd.LimitOverrides == nil || upd.LimitOverrides.MaxSitesPerUser != nil {
		t.Fatalf("reset %+v", upd.LimitOverrides)
	}
}
