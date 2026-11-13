package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

// TestUploadLinkOnAPIHost checks the wiring of upload links on the public
// listener: the page, a file upload without a bearer token, the consent page
// static files next to it.
func TestUploadLinkOnAPIHost(t *testing.T) {
	ctx := context.Background()
	a, err := build(ctx, testConfig(t, t.TempDir()), quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	admin, err := a.db.GetUserByEmail(ctx, "admin@pp.test")
	if err != nil {
		t.Fatal(err)
	}
	info, err := a.svc.BeginUpload(ctx, service.Actor{User: admin, IP: "127.0.0.1"}, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := info.LinkURL[strings.Index(info.LinkURL, "/u/"):]
	do := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.public.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rec
	}
	if rec := do(http.MethodGet, path+"?ui_lang=en", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "Drop a folder or files here") {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPut, path+"/index.html", "<h1>x</h1>"); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/oauth/static/upload.js", ""); rec.Code != http.StatusOK {
		t.Fatalf("script: %d", rec.Code)
	}
	if rec := do(http.MethodGet, path+"/index.html", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("files are not readable through the link: %d", rec.Code)
	}
}
