package render

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const pngMagic = "\x89PNG\r\n\x1a\n"

func TestScreenshotSendsGotenbergForm(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != screenshotPath || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		got = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			got[k] = v[0]
		}
		_, _ = io.WriteString(w, pngMagic+"data")
	}))
	defer srv.Close()
	data, err := New(srv.URL+"/", nil).Screenshot(context.Background(),
		Shot{URL: "http://control:8083/", Token: "tok", Width: 390, Height: 844})
	if err != nil || string(data) != pngMagic+"data" {
		t.Fatalf("%q %v", data, err)
	}
	if got["url"] != "http://control:8083/" || got["width"] != "390" || got["height"] != "844" ||
		got["clip"] != "true" || got["format"] != "png" {
		t.Fatalf("form %v", got)
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(got["extraHttpHeaders"]), &headers); err != nil || headers[TokenHeader] != "tok" {
		t.Fatalf("headers %q %v", got["extraHttpHeaders"], err)
	}
}

func TestScreenshotErrors(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status":  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
		"not png": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>") },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := New(srv.URL, nil).Screenshot(context.Background(), Shot{URL: "http://x/", Width: 1, Height: 1})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err %v", err)
			}
		})
	}
	_, err := New("http://127.0.0.1:1", nil).Screenshot(context.Background(), Shot{URL: "http://x/"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestHandlerServesOnlyTokenRelease(t *testing.T) {
	files := fstest.MapFS{"index.html": {Data: []byte("<h1>hi</h1>")}, "css/a.css": {Data: []byte("a{}")}}
	done := 0
	h := Handler(func(token string) (fs.FS, func(), bool) {
		if token != "good" {
			return nil, nil, false
		}
		return files, func() { done++ }, true
	})
	get := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set(TokenHeader, token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(http.MethodGet, "/", "good"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("index: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(http.MethodGet, "/css/a.css", "good"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("css: %d", rec.Code)
	}
	for _, tok := range []string{"", "bad"} {
		if rec := get(http.MethodGet, "/", tok); rec.Code != http.StatusNotFound {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	if rec := get(http.MethodPost, "/", "good"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", rec.Code)
	}
	if done != 2 {
		t.Fatalf("done called %d times", done)
	}
}
