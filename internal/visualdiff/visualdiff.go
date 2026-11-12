// Package visualdiff compares two screenshots in pure Go: the share of
// changed pixels and a side-by-side "before | after" picture with the
// changes highlighted on the right half.
package visualdiff

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

// Threshold is the largest per-channel difference (0-255) still treated as
// equal, so font antialiasing does not count as a change.
const Threshold = 24

// gap is the width of the divider between the two halves.
const gap = 8

// Result is the outcome of Compare.
type Result struct {
	// ChangedPercent is the share of changed pixels on the common canvas,
	// rounded to hundredths.
	ChangedPercent float64
	// Width and Height are the size of the common canvas.
	Width, Height int
	// Image is "before | after" with changes highlighted on the right.
	Image *image.RGBA
}

var (
	highlight = color.RGBA{R: 230, G: 30, B: 60, A: 255}
	divider   = color.RGBA{R: 40, G: 40, B: 40, A: 255}
	missing   = color.RGBA{R: 255, G: 255, B: 255, A: 255}
)

// Compare compares before and after. Pixels outside one of the images (when
// sizes differ) count as changed.
func Compare(before, after image.Image) Result {
	bb, ab := before.Bounds(), after.Bounds()
	w, h := max(bb.Dx(), ab.Dx()), max(bb.Dy(), ab.Dy())
	out := image.NewRGBA(image.Rect(0, 0, 2*w+gap, h))
	draw.Draw(out, out.Bounds(), &image.Uniform{C: missing}, image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(w, 0, w+gap, h), &image.Uniform{C: divider}, image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(0, 0, bb.Dx(), bb.Dy()), before, bb.Min, draw.Src)
	changed := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			inB := x < bb.Dx() && y < bb.Dy()
			inA := x < ab.Dx() && y < ab.Dy()
			var a color.RGBA
			if inA {
				a = toRGBA(after.At(ab.Min.X+x, ab.Min.Y+y))
			} else {
				a = missing
			}
			same := inA && inB && near(toRGBA(before.At(bb.Min.X+x, bb.Min.Y+y)), a)
			px := mute(a)
			if !same {
				changed++
				px = blend(a, highlight)
			}
			out.SetRGBA(w+gap+x, y, px)
		}
	}
	pct := 0.0
	if total := w * h; total > 0 {
		pct = math.Round(float64(changed)*10000/float64(total)) / 100
	}
	return Result{ChangedPercent: pct, Width: w, Height: h, Image: out}
}

// ComparePNG decodes two PNG images and compares them.
func ComparePNG(before, after []byte) (Result, error) {
	b, err := png.Decode(bytes.NewReader(before))
	if err != nil {
		return Result{}, fmt.Errorf("decode first screenshot: %w", err)
	}
	a, err := png.Decode(bytes.NewReader(after))
	if err != nil {
		return Result{}, fmt.Errorf("decode second screenshot: %w", err)
	}
	return Compare(b, a), nil
}

// EncodePNG encodes img with fast compression.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

func toRGBA(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)} //nolint:gosec // 16-bit to 8-bit channel
}

func near(x, y color.RGBA) bool {
	return diff(x.R, y.R) <= Threshold && diff(x.G, y.G) <= Threshold &&
		diff(x.B, y.B) <= Threshold && diff(x.A, y.A) <= Threshold
}

func diff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// mute fades an unchanged pixel towards white so changes stand out.
func mute(c color.RGBA) color.RGBA {
	f := func(v uint8) uint8 { return uint8((int(v) + 2*255) / 3) } //nolint:gosec // stays within 0-255
	return color.RGBA{R: f(c.R), G: f(c.G), B: f(c.B), A: 255}
}

// blend mixes c with the highlight colour at 60 %.
func blend(c, hl color.RGBA) color.RGBA {
	f := func(a, b uint8) uint8 { return uint8((2*int(a) + 3*int(b)) / 5) } //nolint:gosec // stays within 0-255
	return color.RGBA{R: f(c.R, hl.R), G: f(c.G, hl.G), B: f(c.B, hl.B), A: 255}
}
