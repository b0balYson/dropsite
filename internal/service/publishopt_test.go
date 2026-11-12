package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/optimize"
)

func testPhoto(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	rng := rand.New(rand.NewPCG(3, 4))
	for y := range 300 {
		for x := range 400 {
			n := uint8(rng.IntN(10))
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x/2) + n, G: uint8(y/2) + n, B: 90 + n, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

const optPage = `<!doctype html><html><head><title>t</title></head><body><img src="hero.jpg" alt="x"></body></html>`

func TestPublishOptimizesRelease(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	v, _ := e.svc.CreateSite(ctx, u, "p", "promo")
	host := v.Site.ID + ".pp.test"
	photo := testPhoto(t)
	res, err := e.svc.Deploy(ctx, u, "promo", zipOf(t, map[string]string{
		"index.html": optPage, "hero.jpg": photo, ".dropsite/static/robots.txt": "planted",
	}), domain.SourceCLI)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.FileCount != 2 || res.Deploy.SizeBytes != int64(len(optPage)+len(photo)) {
		t.Fatalf("generated files counted: %+v", res.Deploy)
	}
	if b, err := e.served(host, "hero.jpg"); err != nil || b != photo {
		t.Fatal("original image changed")
	}
	if b, err := e.served(host, optimize.VariantPath("hero.jpg")); err != nil || len(b) >= len(photo) {
		t.Fatalf("no variant: %v", err)
	}
	robots, err := e.served(host, optimize.StaticPath(optimize.RobotsFile))
	if err != nil || !strings.Contains(robots, "Sitemap: https://promo.pp.test/sitemap.xml") {
		t.Fatalf("robots.txt %q %v", robots, err)
	}
	if sm, _ := e.served(host, optimize.StaticPath(optimize.SitemapFile)); !strings.Contains(sm, "<loc>https://promo.pp.test/</loc>") {
		t.Fatalf("sitemap %q", sm)
	}

	// The generated directory is not part of the site's files.
	l, err := e.svc.ListSiteFiles(ctx, u, "promo", "", "")
	if err != nil || l.TotalFiles != 2 {
		t.Fatalf("list %+v %v", l, err)
	}
	r, err := e.svc.ReadSiteFiles(ctx, u, "promo", "", []string{optimize.StaticPath(optimize.RobotsFile)})
	if err != nil || len(r.Files) != 0 || len(r.Missing) != 1 {
		t.Fatalf("read generated %+v %v", r, err)
	}

	// An edit replaces the image: the variant is rebuilt from the new file.
	small := "not a photo anymore " + strings.Repeat("x", 9000)
	up, err := e.svc.UpdateSiteFiles(ctx, u, "promo", res.Deploy.ID, FileChanges{
		Put: []InlineFile{{Path: "hero.jpg", Content: small}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Deploy.FileCount != 2 {
		t.Fatalf("edit counted generated files: %+v", up.Deploy)
	}
	if _, err := e.served(host, optimize.VariantPath("hero.jpg")); err == nil {
		t.Fatal("stale variant kept after the image was replaced")
	}
	if _, err := e.served(host, optimize.StaticPath(optimize.RobotsFile)); err != nil {
		t.Fatal("fallbacks not regenerated after the edit")
	}

	// Counter changes copy the release and keep its generated files.
	if _, err := e.svc.SetSiteCounters(ctx, u, "promo", domain.Counters{MetrikaID: "12345"}, domain.SourceAPI); err != nil {
		t.Fatal(err)
	}
	if _, err := e.served(host, optimize.StaticPath(optimize.RobotsFile)); err != nil {
		t.Fatal("generated files lost on counter re-release")
	}
	cur, _ := e.svc.GetSite(ctx, u, "promo")
	if cur.Current.FileCount != 2 {
		t.Fatalf("re-release counted generated files: %+v", cur.Current)
	}
}

func TestPublishSettingsSwitches(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	other := e.user("bob@pp.test")
	v, _ := e.svc.CreateSite(ctx, u, "p", "promo")
	host := v.Site.ID + ".pp.test"

	got, err := e.svc.GetPublishSettings(ctx, u, "promo")
	if err != nil || !got.Optimize || !got.InstallationEnabled || !got.Effective {
		t.Fatalf("defaults %+v %v", got, err)
	}
	_, err = e.svc.GetPublishSettings(ctx, other, "promo")
	wantNotFound(t, err)
	_, err = e.svc.SetPublishSettings(ctx, other, "promo", false)
	wantNotFound(t, err)
	got, err = e.svc.SetPublishSettings(ctx, u, "promo", false)
	if err != nil || got.Optimize || got.Effective {
		t.Fatalf("off %+v %v", got, err)
	}
	deploy(t, e, u, "promo", optPage)
	if _, err := e.served(host, optimize.Dir); err == nil {
		t.Fatal("optimized although the site turned it off")
	}

	off := newEnv(t, func(c *config.Config) { c.PublishOptimize = false })
	w := off.user("ann@pp.test")
	v, _ = off.svc.CreateSite(ctx, w, "p", "promo")
	got, _ = off.svc.GetPublishSettings(ctx, w, "promo")
	if !got.Optimize || got.InstallationEnabled || got.Effective {
		t.Fatalf("installation off %+v", got)
	}
	deploy(t, off, w, "promo", optPage)
	if _, err := off.served(v.Site.ID+".pp.test", optimize.Dir); err == nil {
		t.Fatal("optimized although the installation turned it off")
	}
}

func TestPublishNoindexSiteGetsNoSitemap(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	u := e.user("ann@pp.test")
	v, _ := e.svc.CreateSite(ctx, u, "p", "promo")
	on := true
	if _, err := e.svc.UpdateSite(ctx, u, "promo", SiteUpdate{Noindex: &on}); err != nil {
		t.Fatal(err)
	}
	deploy(t, e, u, "promo", optPage)
	host := v.Site.ID + ".pp.test"
	if _, err := e.served(host, optimize.StaticPath(optimize.SitemapFile)); err == nil {
		t.Fatal("sitemap for a noindex site")
	}
	if _, err := e.served(host, optimize.StaticPath(optimize.FaviconFile)); err != nil {
		t.Fatal("favicon missing")
	}
}
