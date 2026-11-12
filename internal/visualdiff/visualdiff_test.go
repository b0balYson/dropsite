package visualdiff

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}

func TestIdenticalImagesHaveNoChanges(t *testing.T) {
	a := solid(40, 30, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	r := Compare(a, a)
	if r.ChangedPercent != 0 || r.Width != 40 || r.Height != 30 {
		t.Fatalf("result %+v", r)
	}
	if got := r.Image.Bounds().Dx(); got != 2*40+gap {
		t.Fatalf("side by side width %d", got)
	}
}

func TestSmallNoiseIsIgnored(t *testing.T) {
	a := solid(10, 10, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	b := solid(10, 10, color.RGBA{R: 100 + Threshold, G: 100, B: 100, A: 255})
	if r := Compare(a, b); r.ChangedPercent != 0 {
		t.Fatalf("noise counted: %v", r.ChangedPercent)
	}
}

func TestChangedShare(t *testing.T) {
	a := solid(10, 10, color.White)
	b := solid(10, 10, color.White)
	draw.Draw(b, image.Rect(0, 0, 5, 5), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	r := Compare(a, b)
	if r.ChangedPercent != 25 {
		t.Fatalf("changed %v, want 25", r.ChangedPercent)
	}
	// The changed pixel on the right half is highlighted, an unchanged one muted.
	if got := r.Image.RGBAAt(10+gap, 0); got.R <= got.G {
		t.Fatalf("change not highlighted: %+v", got)
	}
	if got := r.Image.RGBAAt(10+gap+9, 9); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("unchanged pixel %+v", got)
	}
}

func TestDifferentSizesCountMissingAreaAsChanged(t *testing.T) {
	a := solid(10, 10, color.White)
	b := solid(10, 20, color.White)
	r := Compare(a, b)
	if r.Height != 20 || r.ChangedPercent != 50 {
		t.Fatalf("result %+v", r)
	}
}

func TestPNGRoundTrip(t *testing.T) {
	a := solid(4, 4, color.White)
	data, err := EncodePNG(a)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ComparePNG(data, data)
	if err != nil || r.ChangedPercent != 0 {
		t.Fatalf("%v %v", r.ChangedPercent, err)
	}
	if _, err := ComparePNG([]byte("x"), data); err == nil {
		t.Fatal("bad png accepted")
	}
}
