package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

type releaseRT string

func (tag releaseRT) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{"tag_name":"v` + string(tag) + `"}`))}, nil
}

func TestUpdatesEndpoints(t *testing.T) {
	h := newHarness(t, testenv.WithRelease("0.4.0", releaseRT("0.5.0")))
	_, userTok := h.user("u@pp.test")

	// A user's "check" reads the stored facts only: nothing checked yet.
	u := decodeAs[updatesDTO](t, h.do(http.MethodPost, "/api/v1/updates/check", userTok, nil))
	if u.UpdateAvailable || u.LatestVersion != "" || u.IsAdmin || u.UpdatePanelURL != "" || u.HelperInstalled != nil {
		t.Fatalf("user check: %+v", u)
	}
	a := decodeAs[updatesDTO](t, h.do(http.MethodPost, "/api/v1/updates/check", h.adminToken, nil))
	if !a.UpdateAvailable || a.LatestVersion != "0.5.0" || !a.IsAdmin || a.HelperInstalled == nil ||
		!strings.HasSuffix(a.UpdatePanelURL, "/admin/settings#updates-h") || a.CheckedAt == nil {
		t.Fatalf("admin check: %+v", a)
	}
	u = decodeAs[updatesDTO](t, h.do(http.MethodGet, "/api/v1/updates", userTok, nil))
	if !u.UpdateAvailable || u.ReleaseNotesURL != "https://github.com/b0balYson/dropsite/releases/tag/v0.5.0" || u.CurrentVersion != "0.4.0" {
		t.Fatalf("user facts: %+v", u)
	}
	asAssistant := withHeader("X-Dropsite-Assistant", "mcp")
	for i := 0; i < 4; i++ {
		wantStatus(t, h.do(http.MethodPost, "/api/v1/updates/check", h.adminToken, nil, asAssistant), http.StatusOK)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/updates/check", h.adminToken, nil), http.StatusTooManyRequests, domain.CodeRateLimited)
	wantStatus(t, h.do(http.MethodGet, "/api/v1/updates", "", nil), http.StatusUnauthorized)
	if _, err := os.Stat(filepath.Join(h.env.StateDir, "update", "request")); err == nil {
		t.Fatal("an API call wrote an update request")
	}
}
