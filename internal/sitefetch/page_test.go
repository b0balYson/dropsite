package sitefetch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

var htmlOnly = GetOptions{Types: []string{"text/html"}, MaxBytes: 1 << 10, UserAgent: "test-agent"}

func TestGetPageFollowsSafeRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", handler())
	var ua string
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/page", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/ua", func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html></html>"))
	})
	r := newRig(t, mux, 5*time.Second)
	doc, err := r.client.Get(context.Background(), "https://example.com/moved", htmlOnly)
	if err != nil {
		t.Fatal(err)
	}
	if doc.URL != "https://example.com/page" || doc.ContentType != "text/html" || string(doc.Data) != "<html>" {
		t.Fatalf("doc %+v", doc)
	}
	if _, err := r.client.Get(context.Background(), "https://example.com/ua", htmlOnly); err != nil || ua != "test-agent" {
		t.Fatalf("user agent %q, %v", ua, err)
	}
}

func TestGetRefusals(t *testing.T) {
	r := newRig(t, handler(), 2*time.Second)
	for name, u := range map[string]string{
		"http":           "http://example.com/page",
		"private":        "https://inner.example.com/page",
		"redirect inner": "https://example.com/to-private",
		"redirect meta":  "https://example.com/to-meta",
		"image":          "https://example.com/a.png",
		"too big":        "https://example.com/big",
		"not found":      "https://example.com/missing",
		"rebinding":      "https://rebind.example.com/page",
	} {
		_, err := r.client.Get(context.Background(), u, htmlOnly)
		var fe *Error
		if !errors.As(err, &fe) || fe.Reason == "" {
			t.Errorf("%s: want *Error, got %v", name, err)
		}
		if name == "image" && !strings.Contains(fe.Reason, "image/png") {
			t.Errorf("image reason %q", fe.Reason)
		}
	}
}
