package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadLinkEndpoints(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("link@pp.test")
	rec := h.do(http.MethodPost, "/api/v1/uploads", tok, map[string]string{"title": "Linked"})
	wantStatus(t, rec, http.StatusCreated)
	up := decodeAs[uploadDTO](t, rec)
	if up.UploadURLExpiresAt == nil || !strings.HasPrefix(up.UploadURL, "https://") {
		t.Fatalf("upload: %+v", up)
	}
	path := up.UploadURL[strings.Index(up.UploadURL, "/u/"):]
	token := strings.TrimPrefix(path, "/u/")

	// One file by PUT, no bearer token.
	rec = h.do(http.MethodPut, path+"/index.html", "", strings.NewReader("<h1>hi</h1>"))
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[linkReceiptDTO](t, rec)
	if got.Received != 1 || got.Upload.Files != 1 || !strings.Contains(got.Message, "commit_upload") {
		t.Fatalf("put: %+v", got)
	}
	if rec.Header().Get("X-Robots-Tag") == "" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers %v", rec.Header())
	}
	wantError(t, h.do(http.MethodPut, path+"/.env", "", strings.NewReader("K=1")), http.StatusBadRequest, domain.CodeInvalidInput)

	// A folder as a gzip-compressed tar stream.
	body := tarGz(t, map[string]string{"./css/a.css": "a{}", "./.npmrc": "secret", "./img/x.png": "png"})
	wantError(t, h.do(http.MethodPost, path, "", bytes.NewReader(body), withHeader("Content-Type", "text/plain")),
		http.StatusBadRequest, domain.CodeInvalidInput)
	rec = h.do(http.MethodPost, path, "", bytes.NewReader(body), withHeader("Content-Type", "application/x-tar"))
	wantStatus(t, rec, http.StatusOK)
	got = decodeAs[linkReceiptDTO](t, rec)
	if got.Received != 2 || got.Upload.Files != 3 || len(got.Skipped) != 1 || got.Skipped[0].Path != ".npmrc" {
		t.Fatalf("tar: %+v", got)
	}

	// Commit still needs the assistant's own token; then the link is dead.
	rec = h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/commit", tok, nil)
	wantStatus(t, rec, http.StatusCreated)
	wantError(t, h.do(http.MethodPut, path+"/late.txt", "", strings.NewReader("x")), http.StatusNotFound, domain.CodeUploadLinkInvalid)
	wantError(t, h.do(http.MethodPut, "/u/"+strings.Repeat("A", 43)+"/a.txt", "", strings.NewReader("x")),
		http.StatusNotFound, domain.CodeUploadLinkInvalid)

	// The token never reaches the control log.
	if strings.Contains(h.logs.String(), token) {
		t.Fatal("upload link token in the log")
	}
	if !strings.Contains(h.logs.String(), routeLinkPut) {
		t.Fatal("link requests are not logged by route")
	}
}

func TestRedactPath(t *testing.T) {
	for in, want := range map[string]string{
		"/u/abc":          "/u/REDACTED",
		"/u/abc/img/x.js": "/u/REDACTED/img/x.js",
		"/api/v1/me":      "/api/v1/me",
	} {
		if got := redactPath(in); got != want {
			t.Errorf("redactPath(%q) = %q, want %q", in, got, want)
		}
	}
}
