package api

import (
	"net/http"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

func TestSiteDataReadsAreRateLimited(t *testing.T) {
	f := newFormHarness(t)
	base := "/api/v1/sites/" + f.site.ID + "/collections"
	wantStatus(t, f.do(http.MethodPost, base, f.owner, map[string]any{"name": "menu"}), http.StatusCreated)
	for i := range service.DataReadsPerIP {
		path := "/_dropsite/data/menu"
		if i%2 == 1 {
			path += "/nope" // reads of one document count too
		}
		if rec := f.visitor(http.MethodGet, path, "", ""); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("read %d limited", i)
		}
	}
	rec := f.visitor(http.MethodGet, "/_dropsite/data/menu", "", "")
	wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// The owner API is not affected.
	wantStatus(t, f.do(http.MethodGet, base+"/menu/documents", f.owner, nil), http.StatusOK)
}
