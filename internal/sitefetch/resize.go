package sitefetch

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
)

// Image limits: wider images are scaled down to MaxImageWidth; images above
// maxDecodePixels are kept as they are, never decoded (memory bound).
const (
	MaxImageWidth   = 2000
	maxDecodePixels = 16_000_000
	jpegQuality     = 85
)

// Downscale returns a smaller re-encoding of a JPEG or PNG wider than
// MaxImageWidth, or false when the image is kept as it is (other format,
// narrow enough, too large to decode, or the result would not be smaller).
func Downscale(data []byte, contentType string) ([]byte, bool) {
	if contentType != "image/jpeg" && contentType != "image/png" {
		return nil, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= MaxImageWidth || cfg.Height <= 0 ||
		int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return nil, false
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	h := max(1, (cfg.Height*MaxImageWidth+cfg.Width/2)/cfg.Width)
	dst := boxResize(src, MaxImageWidth, h)
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality})
	case "png":
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst)
	default:
		return nil, false
	}
	if err != nil || buf.Len() >= len(data) {
		return nil, false
	}
	return buf.Bytes(), true
}

// boxResize scales src down to w×h by averaging the source pixels that each
// destination pixel covers (area average on premultiplied RGBA).
func boxResize(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	}
	sw, sh := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				row := rgba.Pix[sy*rgba.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r += uint64(p[0])
					g += uint64(p[1])
					bl += uint64(p[2])
					a += uint64(p[3])
					n++
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n) //nolint:gosec // averages of bytes fit in a byte
		}
	}
	return dst
}
