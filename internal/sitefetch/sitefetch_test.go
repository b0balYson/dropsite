package sitefetch

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ips, ok := f[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no such host")
}

// rig serves on 127.0.0.1 with a certificate for example.com. The test
// policy treats 127.0.0.1 as "public" and everything else as private, so
// names resolving elsewhere must be refused.
type rig struct {
	srv    *httptest.Server
	client *Client
}

func newRig(t *testing.T, h http.Handler, timeout time.Duration) *rig {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	loop := netip.MustParseAddr("127.0.0.1")
	c := New(Options{
		Resolver: fakeResolver{
			"example.com":        {loop},
			"rebind.example.com": {loop, netip.MustParseAddr("10.0.0.7")},
			"inner.example.com":  {netip.MustParseAddr("127.0.0.2")},
			"meta.example.com":   {netip.MustParseAddr("169.254.169.254")},
		},
		Allow:   func(a netip.Addr) bool { return a.Unmap() == loop },
		Port:    port,
		RootCAs: pool,
		Timeout: timeout,
	})
	return &rig{srv: srv, client: c}
}

func pngOf(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/a.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngOf(4, 4))
	})
	mux.HandleFunc("/font", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("wOF2fontdata"))
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, MaxFileBytes+10))
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	mux.HandleFunc("/to-private", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://inner.example.com/a.png", http.StatusFound)
	})
	mux.HandleFunc("/to-meta", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://meta.example.com/latest", http.StatusFound)
	})
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/a.png", http.StatusFound)
	})
	mux.HandleFunc("/to-self", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/a.png", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/loop", http.StatusFound)
	})
	return mux
}

func TestFetchOK(t *testing.T) {
	r := newRig(t, handler(), 5*time.Second)
	files, err := r.client.Fetch(context.Background(), []Request{
		{URL: "https://example.com/a.png", Path: "img/a.png"},
		{URL: "https://example.com/font", Path: "fonts/x.woff2"},
		{URL: "https://example.com/to-self", Path: "img/b.png"},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].Path != "img/a.png" || files[0].ContentType != "image/png" || string(files[1].Data) != "wOF2fontdata" {
		t.Fatalf("files: %+v", files)
	}
}

func TestFetchRefusals(t *testing.T) {
	r := newRig(t, handler(), 2*time.Second)
	cases := map[string]Request{
		"http":            {URL: "http://example.com/a.png", Path: "a.png"},
		"port":            {URL: "https://example.com:8443/a.png", Path: "a.png"},
		"userinfo":        {URL: "https://u:p@example.com/a.png", Path: "a.png"},
		"ip literal":      {URL: "https://127.0.0.1/a.png", Path: "a.png"},
		"localhost":       {URL: "https://localhost/a.png", Path: "a.png"},
		"metadata ip":     {URL: "https://169.254.169.254/latest", Path: "a.png"},
		"private name":    {URL: "https://inner.example.com/a.png", Path: "a.png"},
		"dns rebinding":   {URL: "https://rebind.example.com/a.png", Path: "a.png"},
		"redirect inner":  {URL: "https://example.com/to-private", Path: "a.png"},
		"redirect meta":   {URL: "https://example.com/to-meta", Path: "a.png"},
		"redirect http":   {URL: "https://example.com/to-http", Path: "a.png"},
		"redirect loop":   {URL: "https://example.com/loop", Path: "a.png"},
		"html":            {URL: "https://example.com/page", Path: "a.png"},
		"octet as css":    {URL: "https://example.com/font", Path: "x.css"},
		"bad extension":   {URL: "https://example.com/a.png", Path: "index.html"},
		"too big":         {URL: "https://example.com/big", Path: "a.png"},
		"slow":            {URL: "https://example.com/slow", Path: "a.png"},
		"not found":       {URL: "https://example.com/missing", Path: "a.png"},
		"unresolvable":    {URL: "https://nope.example.com/a.png", Path: "a.png"},
		"not a url":       {URL: "::::", Path: "a.png"},
		"too long":        {URL: "https://example.com/" + strings.Repeat("a", 3000), Path: "a.png"},
		"empty host path": {URL: "https:///a.png", Path: "a.png"},
	}
	for name, req := range cases {
		start := time.Now()
		_, err := r.client.Fetch(context.Background(), []Request{req}, 0)
		var fe *Error
		if !errors.As(err, &fe) || fe.Reason == "" {
			t.Errorf("%s: want *Error, got %v", name, err)
		} else {
			t.Logf("%s: %s", name, fe.Reason)
		}
		if time.Since(start) > 4*time.Second {
			t.Errorf("%s: took %v", name, time.Since(start))
		}
	}
}

func TestFetchCallLimits(t *testing.T) {
	r := newRig(t, handler(), 5*time.Second)
	many := make([]Request, MaxURLs+1)
	for i := range many {
		many[i] = Request{URL: "https://example.com/a.png", Path: "a.png"}
	}
	if _, err := r.client.Fetch(context.Background(), many, 0); err == nil {
		t.Fatal("too many urls accepted")
	}
	// The per-file cap follows the caller (owner's MAX_FILE_BYTES).
	_, err := r.client.Fetch(context.Background(), []Request{{URL: "https://example.com/font", Path: "f.woff"}}, 4)
	var fe *Error
	if !errors.As(err, &fe) || !strings.Contains(fe.Reason, "larger") {
		t.Fatalf("per-file cap: %v", err)
	}
}

func TestDownscale(t *testing.T) {
	big := pngOf(2400, 600)
	out, ok := Downscale(big, "image/png")
	if !ok {
		t.Fatal("not resized")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != MaxImageWidth || cfg.Height != 500 {
		t.Fatalf("resized: %+v %v", cfg, err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2600, 400))
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, img, &jpeg.Options{Quality: 100})
	if out, ok := Downscale(jb.Bytes(), "image/jpeg"); ok {
		if cfg, _, _ := image.DecodeConfig(bytes.NewReader(out)); cfg.Width != MaxImageWidth {
			t.Fatalf("jpeg width %d", cfg.Width)
		}
	}
	if _, ok := Downscale(pngOf(100, 100), "image/png"); ok {
		t.Fatal("small image resized")
	}
	if _, ok := Downscale([]byte("garbage"), "image/png"); ok {
		t.Fatal("garbage resized")
	}
	if _, ok := Downscale(big, "image/gif"); ok {
		t.Fatal("gif resized")
	}
}

func TestAllowedPath(t *testing.T) {
	for p, want := range map[string]bool{"a.PNG": true, "f/x.woff2": true, "s.css": true, "a.js": true, "index.html": false, "x": false} {
		if AllowedPath(p) != want {
			t.Errorf("AllowedPath(%s)", p)
		}
	}
}
