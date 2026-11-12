package optimize

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"path"
	"strings"

	"github.com/deepteams/webp"
	xwebp "golang.org/x/image/webp"
)

// Image conversion limits. A 16 MP image decodes to at most 64 MB of RGBA,
// which together with encoder buffers fits a 1 GB server comfortably.
const (
	// MinImageBytes skips tiny images where WebP gains nothing noticeable.
	MinImageBytes = 8 << 10
	// MaxImagePixels skips larger images instead of decoding them.
	MaxImagePixels = 16_000_000
	// keepRatio: a variant is kept only if it is at most 90 % of the original.
	keepNum, keepDen = 9, 10
	lossyQuality     = 80
	encodeMethod     = 4
)

// errSkip marks an image that is deliberately left without a variant.
var errSkip = errors.New("skipped")

// VariantPath is the generated WebP path of a release image path.
func VariantPath(p string) string { return WebPDir + "/" + p + ".webp" }

func (r *runner) images(list []imageFile) {
	for _, f := range list {
		if f.size < MinImageBytes {
			continue
		}
		if r.expired() {
			r.res.TimedOut = true
			return
		}
		r.res.Images.Candidates++
		n, err := r.convert(f)
		switch {
		case errors.Is(err, errSkip):
		case err != nil:
			r.fail("convert image", f.path, err)
		default:
			r.res.Images.Converted++
			r.res.Images.BytesBefore += f.size
			r.res.Images.BytesAfter += n
		}
	}
}

// convert writes the WebP variant of one image and returns its size. A
// panic in a decoder or encoder is turned into an error.
func (r *runner) convert(f imageFile) (n int64, err error) {
	defer func() {
		if p := recover(); p != nil {
			n, err = 0, fmt.Errorf("panic: %v", p)
		}
	}()
	data, err := r.root.ReadFile(f.path)
	if err != nil {
		return 0, err
	}
	out, err := Encode(data, strings.ToLower(path.Ext(f.path)))
	if err != nil {
		return 0, err
	}
	if int64(len(out))*keepDen > f.size*keepNum {
		return 0, errSkip
	}
	if err := r.write(VariantPath(f.path), out); err != nil {
		return 0, err
	}
	return int64(len(out)), nil
}

// Encode turns a JPEG (lossy) or PNG (lossless) file into WebP of the same
// size and checks the result with an independent decoder. ext is the
// lower-case file extension; content that does not match it is skipped.
func Encode(data []byte, ext string) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errSkip
	}
	want := "jpeg"
	if ext == ".png" {
		want = "png"
	}
	if format != want || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return nil, errSkip
	}
	img, err := decode(bytes.NewReader(data), format)
	if err != nil {
		return nil, errSkip
	}
	opts := webp.DefaultOptions()
	opts.Method = encodeMethod
	if format == "png" {
		opts.Lossless = true
	} else {
		opts.Quality = lossyQuality
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, opts); err != nil {
		return nil, fmt.Errorf("encode webp: %w", err)
	}
	if err := verify(buf.Bytes(), cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(r io.Reader, format string) (image.Image, error) {
	if format == "png" {
		return png.Decode(r)
	}
	return jpeg.Decode(r)
}

// verify decodes the variant with golang.org/x/image/webp, independent of
// the encoder, and checks its size.
func verify(data []byte, w, h int) error {
	img, err := xwebp.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("verify webp: %w", err)
	}
	if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
		return fmt.Errorf("verify webp: got %dx%d, want %dx%d", b.Dx(), b.Dy(), w, h)
	}
	return nil
}
