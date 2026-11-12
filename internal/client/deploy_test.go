package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDeployStreamsMultipart(t *testing.T) {
	payload := bytes.Repeat([]byte("abc"), 200_000)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/promo/deploys" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength != -1 {
			t.Errorf("expected a streamed (chunked) body, ContentLength = %d", r.ContentLength)
		}
		f, hdr, err := r.FormFile("archive")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			return
		}
		defer f.Close()
		got, _ := io.ReadAll(f)
		if hdr.Filename != "site.tar.gz" || !bytes.Equal(got, payload) {
			t.Errorf("filename %q, %d bytes", hdr.Filename, len(got))
		}
		writeJSON(w, 201, map[string]any{
			"deploy_id": "d1", "url": "https://promo.example", "urls": []string{"https://promo.example"},
			"deploy": map[string]any{"id": "d1", "current": true}, "site": map[string]any{"id": "s1"},
		})
	})
	var last atomic.Int64
	res, err := c.Deploy(context.Background(), "promo", bytes.NewReader(payload), func(n int64) { last.Store(n) })
	if err != nil {
		t.Fatal(err)
	}
	if res.DeployID != "d1" || res.URL != "https://promo.example" || res.Site.ID != "s1" || !res.Deploy.Current {
		t.Fatalf("res = %+v", res)
	}
	if last.Load() != int64(len(payload)) {
		t.Fatalf("progress = %d, want %d", last.Load(), len(payload))
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestDeployReaderErrorAbortsUpload(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": "archive_corrupt", "message": "corrupt"}})
	})
	_, err := c.Deploy(context.Background(), "s1", failingReader{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeployEarlyRejection(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 413, map[string]any{"error": map[string]any{"code": "archive_too_large", "message": "too large"}})
	})
	big := io.LimitReader(zeroReader{}, 64<<20)
	_, err := c.Deploy(context.Background(), "s1", big, nil)
	if err == nil {
		t.Fatal("expected an error for an early 413")
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.Code != "archive_too_large" {
		t.Fatalf("err = %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestDeployBadRef(t *testing.T) {
	c := New("http://[::1", "t")
	if _, err := c.Deploy(context.Background(), "s", strings.NewReader(""), nil); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}
