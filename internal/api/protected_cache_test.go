package api

import (
	"net/http"
	"testing"
)

// Answers of a password-protected site on /_dropsite/data and
// /_dropsite/proxy must not be stored by shared caches; open sites keep
// their revalidation headers.
func TestProtectedSiteVisitorAnswersAreNotCached(t *testing.T) {
	f := newFormHarness(t)
	base := "/api/v1/sites/" + f.site.ID + "/collections"
	wantStatus(t, f.do(http.MethodPost, base, f.owner, map[string]any{"name": "menu"}), http.StatusCreated)

	check := func(name, path, want string) {
		t.Helper()
		rec := f.visitor(http.MethodGet, path, "", "")
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s %s: Cache-Control %q, want %q (status %d)", name, path, got, want, rec.Code)
		}
	}
	check("open", "/_dropsite/data/menu", "no-cache")
	check("open", "/_dropsite/data/missing", "no-cache")

	wantStatus(t, f.do(http.MethodPatch, "/api/v1/sites/"+f.site.ID, f.owner,
		map[string]any{"password": "visitor-pass-1"}), http.StatusOK)
	for _, path := range []string{"/_dropsite/data/menu", "/_dropsite/data/missing", "/_dropsite/proxy/nope/chat"} {
		check("protected", path, cacheProtected)
	}
}
