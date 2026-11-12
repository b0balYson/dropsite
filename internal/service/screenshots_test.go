package service

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/visualdiff"
)

// fakeRenderer "renders" a page the way the real renderer reaches it:
// through the render token. The top half of the picture is white for
// pages containing "v1" and black otherwise.
type fakeRenderer struct {
	svc   *Service
	calls int
	fail  bool
}

func (f *fakeRenderer) Screenshot(_ context.Context, shot render.Shot) ([]byte, error) {
	f.calls++
	if f.fail {
		return nil, render.ErrUnavailable
	}
	files, done, ok := f.svc.OpenRenderRelease(shot.Token)
	if !ok {
		return nil, errors.New("token rejected")
	}
	defer done()
	page, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, shot.Width/10, shot.Height/10))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	if string(page) != "v1" {
		top := image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()/2)
		draw.Draw(img, top, &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	}
	return visualdiff.EncodePNG(img)
}

func TestScreenshotsUnavailableWithoutRenderer(t *testing.T) {
	e := newEnv(t)
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	if e.svc.ScreenshotsEnabled() {
		t.Fatal("enabled without RENDERER_URL")
	}
	_, err := e.svc.Screenshot(context.Background(), u, id, "", "")
	wantCode(t, err, domain.CodeRendererUnavailable)
	_, err = e.svc.CompareReleases(context.Background(), u, id, "x", "", "")
	wantCode(t, err, domain.CodeRendererUnavailable)
}

func TestScreenshotAndCompare(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fake := &fakeRenderer{svc: e.svc}
	e.svc.shooter = fake
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, map[string]string{"index.html": "v1"})
	first := e.current(u, id)
	second := e.redeploy(u, id, "v2")

	shot, err := e.svc.Screenshot(ctx, u, id, "", "mobile")
	if err != nil {
		t.Fatal(err)
	}
	if shot.DeployID != second || shot.Width != 390 || len(shot.PNG) == 0 {
		t.Fatalf("shot %+v", shot)
	}
	// Cached: the renderer is not called again for the same release.
	if _, err := e.svc.Screenshot(ctx, u, id, second, "mobile"); err != nil || fake.calls != 1 {
		t.Fatalf("cache: calls %d %v", fake.calls, err)
	}
	// The token is gone after the screenshot.
	if len(e.svc.grants) != 0 {
		t.Fatal("render token left behind")
	}
	_, err = e.svc.Screenshot(ctx, u, id, "", "tablet")
	wantCode(t, err, domain.CodeInvalidInput)
	_, err = e.svc.Screenshot(ctx, e.user("x@pp.test"), id, "", "")
	wantNotFound(t, err)

	diff, err := e.svc.CompareReleases(ctx, u, id, first, "", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if diff.FromDeployID != first || diff.ToDeployID != second || diff.ChangedPercent != 50 || len(diff.PNG) == 0 {
		t.Fatalf("diff %+v", diff.ChangedPercent)
	}
	same, err := e.svc.CompareReleases(ctx, u, id, second, second, "desktop")
	if err != nil || same.ChangedPercent != 0 {
		t.Fatalf("same %v %v", same.ChangedPercent, err)
	}
	_, err = e.svc.CompareReleases(ctx, u, id, "", "", "")
	wantCode(t, err, domain.CodeInvalidInput)
}

func TestRenderTokenExpiresAndRendererFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fake := &fakeRenderer{svc: e.svc, fail: true}
	e.svc.shooter = fake
	u := e.user("u@pp.test")
	id, _, dep := e.published(u, shop)
	_, err := e.svc.Screenshot(ctx, u, id, "", "")
	wantCode(t, err, domain.CodeRendererUnavailable)

	tok := e.svc.grantRender(id, dep)
	if _, done, ok := e.svc.OpenRenderRelease(tok); !ok {
		t.Fatal("fresh token rejected")
	} else {
		done()
	}
	e.clock.Advance(RenderTokenTTL + time.Second)
	if _, _, ok := e.svc.OpenRenderRelease(tok); ok {
		t.Fatal("expired token accepted")
	}
	if _, _, ok := e.svc.OpenRenderRelease(""); ok {
		t.Fatal("empty token accepted")
	}
}

func TestPurgeScreenshots(t *testing.T) {
	e := newEnv(t)
	old := filepath.Join(e.svc.shotDir, "site", "old.png")
	fresh := filepath.Join(e.svc.shotDir, "site", "new.png")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-ScreenshotCacheTTL - time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	e.svc.purgeScreenshots(time.Now())
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old screenshot kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh screenshot removed")
	}
}
