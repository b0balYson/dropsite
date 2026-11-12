//go:build e2e

package e2e

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
)

// e2ePhoto is a noisy JPEG at maximum quality, so its WebP variant is
// clearly smaller.
func e2ePhoto(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 320, 240))
	rng := rand.New(rand.NewPCG(5, 6))
	for y := range 240 {
		for x := range 320 {
			n := uint8(rng.IntN(10))
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x/2) + n, G: uint8(y) + n, B: 80 + n, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestPublishOptimization: WebP variants are served by Accept with Vary,
// fallback files only where the site has none, and the generated directory
// is not reachable directly.
func TestPublishOptimization(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-publish")}

	status, body := owner.json(http.MethodPost, "/sites", map[string]string{"title": "Optimized"})
	must(t, status, http.StatusCreated, body, "create site")
	siteID := str(body, "id")
	photo := e2ePhoto(t)
	status, body = owner.deploy(siteID, makeZip(t, site(map[string]string{
		"index.html":   "<!doctype html><title>x</title><img src=\"img/hero.jpg\" alt=\"hero\">",
		"img/hero.jpg": photo, "200.html": "spa",
	})...))
	must(t, status, http.StatusCreated, body, "deploy")
	host := hostOf(str(body, "url"))
	s.expectBody(host, "/", http.StatusOK, "hero") // waits for the certificate

	resp, got, err := s.get(host, "/img/hero.jpg", "Accept", "image/avif,image/webp,*/*")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Type") != "image/webp" || !strings.Contains(resp.Header.Get("Vary"), "Accept") ||
		len(got) >= len(photo) {
		t.Fatalf("webp variant: %s %q vary %q, %d bytes", resp.Status, resp.Header.Get("Content-Type"),
			resp.Header.Get("Vary"), len(got))
	}
	resp, got, err = s.get(host, "/img/hero.jpg", "Accept", "*/*")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Type") != "image/jpeg" || got != photo || !strings.Contains(resp.Header.Get("Vary"), "Accept") {
		t.Fatalf("original: %s %q", resp.Status, resp.Header.Get("Content-Type"))
	}

	// Generated fallbacks win over the SPA fallback but never over own files.
	s.expectBody(host, "/robots.txt", http.StatusOK, "Sitemap: https://"+host)
	s.expectBody(host, "/sitemap.xml", http.StatusOK, "<loc>https://"+host)
	if resp, _, err = s.get(host, "/favicon.ico"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("favicon: %v %v", resp, err)
	}
	s.expectBody(host, "/.dropsite/static/robots.txt", http.StatusNotFound, "")

	// Turning optimization off: the next release has no generated files.
	status, body = owner.json(http.MethodPatch, "/sites/"+siteID+"/publish-settings", map[string]bool{"optimize": false})
	must(t, status, http.StatusOK, body, "turn off")
	status, body = owner.deploy(siteID, makeZip(t, site(map[string]string{
		"index.html": "<p>plain</p>", "img/hero.jpg": photo, "200.html": "spa",
	})...))
	must(t, status, http.StatusCreated, body, "deploy without optimization")
	s.expectBody(host, "/robots.txt", http.StatusOK, "spa")
	resp, _, err = s.get(host, "/img/hero.jpg", "Accept", "image/webp")
	if err != nil || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("no variant expected: %v %v", resp, err)
	}
}
