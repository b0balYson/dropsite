package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// smallLimits keeps the upload limit around 2 MiB: 1 MiB + 10 KiB + 1 MiB.
func smallLimits() []testenv.Option {
	return []testenv.Option{
		testenv.WithEnv("MAX_SITE_BYTES", "1MiB"),
		testenv.WithEnv("MAX_FILE_BYTES", "100KiB"),
		testenv.WithEnv("MAX_FILES", "10"),
	}
}

func TestDeployArchiveValidationErrors(t *testing.T) {
	h := newHarness(t, smallLimits()...)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")

	p := wantError(t, h.deploy(site.ID, tok, testenv.Zip(t, map[string]string{"index.html": "x", "../evil.html": "x"})),
		http.StatusBadRequest, domain.CodeArchiveTraversal)
	if p.Details["path"] == nil {
		t.Fatalf("details %v", p.Details)
	}
	wantError(t, h.deploy(site.ID, tok, testenv.Zip(t, map[string]string{"page.html": "x"})),
		http.StatusBadRequest, domain.CodeArchiveNoIndex)
	wantError(t, h.deploy(site.ID, tok, []byte("plain text, not an archive")),
		http.StatusBadRequest, domain.CodeArchiveFormat)

	big := testenv.Zip(t, map[string]string{"index.html": strings.Repeat("0", 200<<10)})
	p = wantError(t, h.deploy(site.ID, tok, big), http.StatusRequestEntityTooLarge, domain.CodeArchiveFileTooBig)
	if p.Details["limit"] != float64(100<<10) {
		t.Fatalf("details %v", p.Details)
	}
	// Nothing was published.
	got := decodeAs[siteDTO](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID, tok, nil))
	if got.CurrentDeploy != nil {
		t.Fatal("failed deploy became current")
	}
}

func TestDeployRejectsDeclaredOversizeWithoutReading(t *testing.T) {
	h := newHarness(t, smallLimits()...)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")
	body := &countingReader{r: strings.NewReader("never read")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+site.ID+"/deploys", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.ContentLength = 3 << 20
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	p := wantError(t, rec, http.StatusRequestEntityTooLarge, domain.CodeArchiveTooLarge)
	if p.Details["limit"] == nil || body.n != 0 {
		t.Fatalf("details %v, read %d bytes", p.Details, body.n)
	}
}

func TestDeployStreamingOversize(t *testing.T) {
	h := newHarness(t, smallLimits()...)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")
	limit, err := h.env.Svc.UploadLimit(t.Context(), h.env.Admin, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int64{
		"part over limit":         limit + 1,
		"body over limit+framing": limit + 2*multipartOverhead,
	} {
		t.Run(name, func(t *testing.T) {
			body, ctype := multipartBody(t, "archive", bytes.Repeat([]byte{'z'}, int(size)))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+site.ID+"/deploys", io.MultiReader(bytes.NewReader(body)))
			req.ContentLength = -1 // chunked: size unknown up front
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("Content-Type", ctype)
			rec := httptest.NewRecorder()
			h.h.ServeHTTP(rec, req)
			wantError(t, rec, http.StatusRequestEntityTooLarge, domain.CodeArchiveTooLarge)
		})
	}
}

func TestDeployMalformedRequests(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Site", "")
	path := "/api/v1/sites/" + site.ID + "/deploys"

	body, ctype := multipartBody(t, "file", testenv.Zip(t, index("x")))
	wantError(t, h.do(http.MethodPost, path, tok, bytes.NewReader(body), withHeader("Content-Type", ctype)),
		http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodPost, path, tok, `{"archive":"x"}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodPost, path, tok, strings.NewReader("--x\r\nbroken"),
		withHeader("Content-Type", "multipart/form-data; boundary=x")), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.deploy("missing", tok, testenv.Zip(t, index("x"))), http.StatusNotFound, domain.CodeNotFound)
}

func TestDeployRateLimit(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Busy", "")
	archive := testenv.Zip(t, index("x"))
	for i := range 30 {
		if rec := h.deploy(site.ID, tok, archive); rec.Code != http.StatusCreated {
			t.Fatalf("deploy %d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := h.deploy(site.ID, tok, archive)
	p := wantError(t, rec, http.StatusTooManyRequests, domain.CodeRateLimited)
	ra := rec.Header().Get("Retry-After")
	if ra == "" || ra == "0" || strings.ContainsAny(ra, ".") {
		t.Fatalf("Retry-After %q", ra)
	}
	if p.Details["limit"] != float64(30) {
		t.Fatalf("details %v", p.Details)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}
