package optimize

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	xwebp "golang.org/x/image/webp"
)

// photo renders a smooth image with mild noise, like a photograph.
func photo(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			n := uint8(rng.IntN(12))
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x*255/w) + n, G: uint8(y*255/h) + n, B: uint8((x+y)*127/(w+h)) + n, A: 255})
		}
	}
	return img
}

func jpegBytes(t *testing.T, img image.Image, q int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeTree(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for p, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readGen(t *testing.T, dir, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

const page = `<!doctype html><html lang="en"><head><title>x</title></head><body><img src="img/hero.jpg"></body></html>`

func TestRunCreatesVariantsWithoutTouchingSiteFiles(t *testing.T) {
	big := jpegBytes(t, photo(600, 400), 100)
	files := map[string][]byte{
		"index.html":       []byte(page),
		"img/hero.jpg":     big,
		"img/Shot.JPG":     big,
		"img/diagram.png":  pngBytes(t, photo(300, 200)),
		"img/tiny.png":     pngBytes(t, photo(8, 8)),
		"img/broken.jpg":   bytes.Repeat([]byte("not an image "), 1000),
		"img/fake.png":     big, // JPEG content with a .png name
		"about/index.html": []byte(page),
	}
	dir := writeTree(t, files)
	before := snapshot(t, dir)

	res := Run(context.Background(), dir, Options{SiteURL: "https://a.example.com", SiteID: "abc"})

	after := snapshot(t, dir)
	for p, data := range before {
		if after[p] != data {
			t.Errorf("site file %s changed", p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok && !IsGenerated(p) {
			t.Errorf("unexpected file outside %s: %s", Dir, p)
		}
	}
	for _, p := range []string{"img/hero.jpg", "img/Shot.JPG", "img/diagram.png"} {
		v := readGen(t, dir, VariantPath(p))
		img, err := xwebp.Decode(bytes.NewReader(v))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if int64(len(v))*10 > int64(len(files[p]))*9 {
			t.Errorf("%s: variant %d bytes, original %d", p, len(v), len(files[p]))
		}
		src, _, _ := image.DecodeConfig(bytes.NewReader(files[p]))
		if img.Bounds().Dx() != src.Width || img.Bounds().Dy() != src.Height {
			t.Errorf("%s: size changed to %v", p, img.Bounds())
		}
	}
	for _, p := range []string{"img/tiny.png", "img/broken.jpg", "img/fake.png"} {
		if exists(dir, VariantPath(p)) {
			t.Errorf("%s got a variant", p)
		}
	}
	if res.Images.Converted != 3 || res.Images.BytesAfter >= res.Images.BytesBefore || res.TimedOut {
		t.Errorf("result %+v", res)
	}
}

func TestRunGeneratesFallbacks(t *testing.T) {
	dir := writeTree(t, map[string][]byte{
		"index.html":         []byte(page),
		"about/index.html":   []byte(page),
		"price list.html":    []byte(page),
		"404.html":           []byte(page),
		"200.html":           []byte(page),
		"draft.html":         []byte(`<html><head><meta name="robots" content="NOINDEX, follow"></head></html>`),
		"late.html":          []byte(`<html><body><meta name="robots" content="noindex"></body></html>`),
		"docs/404.html":      []byte(page),
		".well-known/x.html": []byte(page),
	})
	res := Run(context.Background(), dir, Options{SiteURL: "https://a.example.com", SiteID: "abc"})
	slices.Sort(res.Generated)
	if !slices.Equal(res.Generated, []string{FaviconFile, RobotsFile, SitemapFile}) {
		t.Fatalf("generated %v", res.Generated)
	}
	robots := string(readGen(t, dir, StaticPath(RobotsFile)))
	if !strings.Contains(robots, "Sitemap: https://a.example.com/sitemap.xml") || !strings.Contains(robots, "Allow: /") {
		t.Errorf("robots.txt:\n%s", robots)
	}
	sm := string(readGen(t, dir, StaticPath(SitemapFile)))
	for _, want := range []string{"<loc>https://a.example.com/</loc>", "<loc>https://a.example.com/about/</loc>",
		"<loc>https://a.example.com/price%20list.html</loc>", "<loc>https://a.example.com/docs/404.html</loc>",
		"<loc>https://a.example.com/late.html</loc>"} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap lacks %s:\n%s", want, sm)
		}
	}
	for _, bad := range []string{"/404.html<", "/200.html", "draft.html", ".well-known"} {
		if strings.Contains(sm, "a.example.com"+bad) || (bad != "/404.html<" && strings.Contains(sm, bad)) {
			t.Errorf("sitemap lists %s:\n%s", bad, sm)
		}
	}
	ico := readGen(t, dir, StaticPath(FaviconFile))
	if !bytes.HasPrefix(ico, []byte{0, 0, 1, 0, 1, 0, 32, 32}) {
		t.Errorf("favicon header % x", ico[:8])
	}
	if _, err := png.Decode(bytes.NewReader(ico[22:])); err != nil {
		t.Errorf("favicon image: %v", err)
	}
}

func TestRunRespectsOwnFilesAndNoindex(t *testing.T) {
	dir := writeTree(t, map[string][]byte{
		"index.html": []byte(page), "robots.txt": []byte("User-agent: *\nDisallow: /x\n"),
		"sitemap.xml": []byte("<urlset/>"), "favicon.ico": []byte("ico"),
	})
	res := Run(context.Background(), dir, Options{SiteURL: "https://a.example.com", SiteID: "abc"})
	if len(res.Generated) != 0 || exists(dir, Dir) {
		t.Errorf("generated %v despite own files", res.Generated)
	}

	dir = writeTree(t, map[string][]byte{"index.html": []byte(page), "sitemap.xml": []byte("<urlset/>")})
	Run(context.Background(), dir, Options{SiteURL: "https://a.example.com"})
	if r := string(readGen(t, dir, StaticPath(RobotsFile))); !strings.Contains(r, "Sitemap: https://a.example.com/sitemap.xml") {
		t.Errorf("robots.txt should point at the own sitemap:\n%s", r)
	}

	dir = writeTree(t, map[string][]byte{"index.html": []byte(page)})
	res = Run(context.Background(), dir, Options{SiteURL: "https://a.example.com", Noindex: true})
	if !slices.Equal(res.Generated, []string{FaviconFile}) {
		t.Errorf("noindex site generated %v", res.Generated)
	}
}

func TestRunReplacesStaleGeneratedDir(t *testing.T) {
	dir := writeTree(t, map[string][]byte{"index.html": []byte(page), Dir + "/webp/old.png.webp": []byte("stale")})
	Run(context.Background(), dir, Options{})
	if exists(dir, Dir+"/webp/old.png.webp") {
		t.Error("stale generated file kept")
	}
}

func TestRunStopsAtDeadline(t *testing.T) {
	big := jpegBytes(t, photo(300, 200), 100)
	dir := writeTree(t, map[string][]byte{"index.html": []byte(page), "a.jpg": big, "b.jpg": big})
	res := Run(context.Background(), dir, Options{Deadline: time.Now().Add(-time.Second)})
	if !res.TimedOut || res.Images.Converted != 0 || exists(dir, VariantPath("a.jpg")) {
		t.Errorf("result %+v", res)
	}
	if !slices.Equal(res.Generated, []string{FaviconFile}) {
		t.Errorf("fallbacks must not depend on the image budget: %v", res.Generated)
	}
}

func TestEncodeSkipsHugeImages(t *testing.T) {
	// A PNG header claiming 5000x5000 pixels is rejected before decoding.
	img := image.NewGray(image.Rect(0, 0, 5000, 4000))
	if _, err := Encode(pngBytes(t, img), ".png"); err == nil {
		t.Fatal("huge image encoded")
	}
}

func TestRunOnMissingDirDoesNotPanic(t *testing.T) {
	res := Run(context.Background(), filepath.Join(t.TempDir(), "nope"), Options{})
	if res.Failures != 1 {
		t.Errorf("result %+v", res)
	}
}

func TestPageURLPath(t *testing.T) {
	for in, want := range map[string]string{
		"index.html": "/", "a/index.html": "/a/", "a/b.html": "/a/b.html", "ü.html": "/%C3%BC.html",
	} {
		if got := PageURLPath(in); got != want {
			t.Errorf("PageURLPath(%q) = %q, want %q", in, got, want)
		}
	}
}
