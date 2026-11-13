package api

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestInspectReferenceAPI(t *testing.T) {
	h := newHarness(t, testenv.WithPageFetcher(testenv.NewStaticPages()))
	_, tok := h.user("u@pp.test")
	rec := h.do(http.MethodPost, "/api/v1/references/inspect", tok, map[string]string{"url": testenv.ReferenceURL})
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, leak := range []string{"SCRIPT-LEAK", "HEADLINE-LEAK", "Logo"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks %q: %s", leak, body)
		}
	}
	got := decodeAs[referenceDTO](t, rec)
	if got.ColorScheme != "dark" || got.Title != "Reference" || got.Stylesheets.Fetched != 1 || got.Screenshot.Status != "unavailable" ||
		got.Screenshot.ImagePNG != "" {
		t.Fatalf("brief %+v", got)
	}

	wantStatus(t, h.do(http.MethodPost, "/api/v1/references/inspect", tok, map[string]string{"url": "http://reference.example/"}),
		http.StatusBadRequest)
	wantStatus(t, h.do(http.MethodPost, "/api/v1/references/inspect", tok, map[string]string{}), http.StatusBadRequest)
	rec = h.do(http.MethodPost, "/api/v1/references/inspect", tok, map[string]string{"url": testenv.ReferenceURL + "missing"})
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "fetch_failed") {
		t.Fatalf("body %s", rec.Body.String())
	}
	wantStatus(t, h.do(http.MethodPost, "/api/v1/references/inspect", "", map[string]string{"url": testenv.ReferenceURL}),
		http.StatusUnauthorized)
}

func TestInspectReferenceAPIScreenshot(t *testing.T) {
	h := newHarness(t, testenv.WithPageFetcher(testenv.NewStaticPages()), testenv.WithRenderer(pngRenderer{}))
	_, tok := h.user("u@pp.test")
	rec := h.do(http.MethodPost, "/api/v1/references/inspect", tok, map[string]string{"url": testenv.ReferenceURL})
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[referenceDTO](t, rec)
	png, err := base64.StdEncoding.DecodeString(got.Screenshot.ImagePNG)
	if err != nil || got.Screenshot.Status != "included" || got.Screenshot.Width != 1280 || !strings.HasPrefix(string(png), "\x89PNG") {
		t.Fatalf("screenshot %+v %v", got.Screenshot.Status, err)
	}
}
