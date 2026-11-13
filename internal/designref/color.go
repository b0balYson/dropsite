package designref

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// rgb is an opaque-enough color.
type rgb struct{ r, g, b uint8 }

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

// minAlpha drops nearly transparent colors (overlays, shadows).
const minAlpha = 0.35

// minChroma is the channel spread below which a color reads as a tinted gray.
const minChroma = 48

var (
	hexRe   = regexp.MustCompile(`#([0-9a-fA-F]{3,8})\b`)
	funcRe  = regexp.MustCompile(`(?i)\b(rgba?|hsla?)\(([^()]*)\)`)
	wordRe  = regexp.MustCompile(`[a-zA-Z]+`)
	urlFnRe = regexp.MustCompile(`(?i)(url|var)\([^)]*\)|--[\w-]+`)
)

var namedColors = map[string]rgb{
	"white": {255, 255, 255}, "black": {0, 0, 0}, "red": {255, 0, 0}, "green": {0, 128, 0},
	"blue": {0, 0, 255}, "yellow": {255, 255, 0}, "orange": {255, 165, 0}, "purple": {128, 0, 128},
	"gray": {128, 128, 128}, "grey": {128, 128, 128}, "silver": {192, 192, 192}, "navy": {0, 0, 128},
	"teal": {0, 128, 128}, "maroon": {128, 0, 0}, "olive": {128, 128, 0}, "crimson": {220, 20, 60},
	"tomato": {255, 99, 71}, "gold": {255, 215, 0}, "ivory": {255, 255, 240}, "beige": {245, 245, 220},
	"whitesmoke": {245, 245, 245}, "gainsboro": {220, 220, 220}, "lightgray": {211, 211, 211},
	"lightgrey": {211, 211, 211}, "darkgray": {169, 169, 169}, "darkgrey": {169, 169, 169},
	"dimgray": {105, 105, 105}, "dimgrey": {105, 105, 105}, "coral": {255, 127, 80},
	"salmon": {250, 128, 114}, "indigo": {75, 0, 130}, "pink": {255, 192, 203}, "linen": {250, 240, 230},
	"snow": {255, 250, 250}, "aliceblue": {240, 248, 255}, "ghostwhite": {248, 248, 255},
	"royalblue": {65, 105, 225}, "steelblue": {70, 130, 180}, "slategray": {112, 128, 144},
	"darkslategray": {47, 79, 79}, "midnightblue": {25, 25, 112}, "firebrick": {178, 34, 34},
	"seagreen": {46, 139, 87}, "forestgreen": {34, 139, 34}, "tan": {210, 180, 140}, "khaki": {240, 230, 140},
}

// colorsIn returns the colors of a CSS value, in order.
func colorsIn(v string) []rgb {
	v = urlFnRe.ReplaceAllString(v, " ")
	var out []rgb
	for _, m := range hexRe.FindAllStringSubmatch(v, -1) {
		if c, ok := parseHex(m[1]); ok {
			out = append(out, c)
		}
	}
	for _, m := range funcRe.FindAllStringSubmatch(v, -1) {
		if c, ok := parseFunc(strings.ToLower(m[1]), m[2]); ok {
			out = append(out, c)
		}
	}
	rest := funcRe.ReplaceAllString(hexRe.ReplaceAllString(v, " "), " ")
	for _, w := range wordRe.FindAllString(rest, -1) {
		if c, ok := namedColors[strings.ToLower(w)]; ok {
			out = append(out, c)
		}
	}
	return out
}

func parseHex(h string) (rgb, bool) {
	switch len(h) {
	case 3, 4:
		if len(h) == 4 && hexByte(h[3:4]+h[3:4]) < 90 {
			return rgb{}, false
		}
		return rgb{hexByte(h[0:1] + h[0:1]), hexByte(h[1:2] + h[1:2]), hexByte(h[2:3] + h[2:3])}, true
	case 6, 8:
		if len(h) == 8 && hexByte(h[6:8]) < 90 {
			return rgb{}, false
		}
		return rgb{hexByte(h[0:2]), hexByte(h[2:4]), hexByte(h[4:6])}, true
	}
	return rgb{}, false
}

func hexByte(s string) uint8 {
	n, _ := strconv.ParseUint(s, 16, 8)
	return uint8(n)
}

// parseFunc reads rgb(), rgba(), hsl() and hsla() in comma or space syntax.
func parseFunc(name, args string) (rgb, bool) {
	args = strings.NewReplacer(",", " ", "/", " ").Replace(args)
	f := strings.Fields(args)
	if len(f) < 3 {
		return rgb{}, false
	}
	if len(f) >= 4 {
		a, ok := number(f[3], 1)
		if !ok || a < minAlpha {
			return rgb{}, false
		}
	}
	if strings.HasPrefix(name, "rgb") {
		var c [3]uint8
		for i := range 3 {
			n, ok := number(f[i], 255)
			if !ok {
				return rgb{}, false
			}
			c[i] = clampByte(n)
		}
		return rgb{c[0], c[1], c[2]}, true
	}
	h, ok1 := strconv.ParseFloat(strings.TrimSuffix(f[0], "deg"), 64)
	s, ok2 := number(f[1], 1)
	l, ok3 := number(f[2], 1)
	if ok1 != nil || !ok2 || !ok3 {
		return rgb{}, false
	}
	return hslToRGB(h, s, l), true
}

// number parses a plain number or a percentage of scale.
func number(s string, scale float64) (float64, bool) {
	if p, ok := strings.CutSuffix(s, "%"); ok {
		n, err := strconv.ParseFloat(p, 64)
		return n / 100 * scale, err == nil
	}
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil
}

func clampByte(n float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(255, n))))
}

func hslToRGB(h, s, l float64) rgb {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	if s == 0 {
		v := clampByte(l * 255)
		return rgb{v, v, v}
	}
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	conv := func(t float64) uint8 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1.0/6:
			return clampByte((p + (q-p)*6*t) * 255)
		case t < 0.5:
			return clampByte(q * 255)
		case t < 2.0/3:
			return clampByte((p + (q-p)*(2.0/3-t)*6) * 255)
		}
		return clampByte(p * 255)
	}
	return rgb{conv(h + 1.0/3), conv(h), conv(h - 1.0/3)}
}

// luminance is the relative luminance (0 black .. 1 white).
func (c rgb) luminance() float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// saturation is the HSL saturation (0 gray .. 1 vivid).
func (c rgb) saturation() float64 {
	r, g, b := float64(c.r)/255, float64(c.g)/255, float64(c.b)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	if mx == mn {
		return 0
	}
	l := (mx + mn) / 2
	if l > 0.5 {
		return (mx - mn) / (2 - mx - mn)
	}
	return (mx - mn) / (mx + mn)
}

// vivid reports whether the color reads as a hue rather than a gray.
func (c rgb) vivid() bool {
	lum := c.luminance()
	hi := max(c.r, c.g, c.b)
	lo := min(c.r, c.g, c.b)
	return c.saturation() >= 0.3 && int(hi)-int(lo) >= minChroma && lum > 0.02 && lum < 0.9
}
