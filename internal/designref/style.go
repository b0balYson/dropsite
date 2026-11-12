package designref

import (
	"regexp"
	"strconv"
	"strings"
)

// Bounds of the style statistics.
const (
	maxLengths    = 5000
	maxVarResolve = 4
	minFontPx     = 8
	maxFontPx     = 200
	maxSpacingPx  = 400
	pillPx        = 999
	basePx        = 16
)

var (
	varRe     = regexp.MustCompile(`var\(\s*(--[\w-]+)\s*(?:,([^()]*))?\)`)
	lengthRe  = regexp.MustCompile(`(-?\d*\.?\d+)(px|rem|em|pt)\b`)
	fontRe    = regexp.MustCompile(`(\d*\.?\d+)(px|rem|em|pt|%)(\s*/\s*\S+)?\s+(.+)$`)
	tokenSep  = regexp.MustCompile(`[^a-z0-9]+`)
	iconFonts = []string{"icon", "awesome", "material symbols", "glyph", "dashicons"}
)

type colorStat struct {
	c                                          rgb
	bg, rootBg, text, rootText, border, accent int
	other                                      int
}

func (s *colorStat) total() int { return s.bg + s.text + s.border + s.accent + s.other }

type fontStat struct {
	heading, body, other, order int
}

// styleStats accumulates what the stylesheets say about the design.
type styleStats struct {
	vars        map[string]string
	varOrder    []string
	colors      map[rgb]*colorStat
	fonts       map[string]*fontStat
	fontFaces   map[string]bool
	sizes       map[float64]int
	spacing     []float64
	radius      []float64
	pill        int
	shadows     int
	maxShadow   float64
	sticky      bool
	heroBgImage bool
	darkMedia   bool
	truncated   bool
	ruleCount   int
}

func newStyleStats() *styleStats {
	return &styleStats{
		vars: map[string]string{}, colors: map[rgb]*colorStat{}, fonts: map[string]*fontStat{},
		fontFaces: map[string]bool{}, sizes: map[float64]int{},
	}
}

func selTokens(sel string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokenSep.Split(strings.ToLower(sel), -1) {
		if t != "" {
			out[t] = true
		}
	}
	return out
}

func hasAny(tok map[string]bool, words ...string) bool {
	for _, w := range words {
		if tok[w] {
			return true
		}
	}
	return false
}

func isRootSel(sel string) bool {
	for _, part := range strings.Split(strings.ToLower(sel), ",") {
		p := strings.TrimSpace(part)
		switch p {
		case ":root", "html", "body", "html body", ":host", "html, body":
			return true
		}
		for _, pre := range []string{":root[", "html[", "html.", "body.", "body["} {
			if strings.HasPrefix(p, pre) {
				return true
			}
		}
	}
	return false
}

func isDarkSel(tok map[string]bool) bool { return hasAny(tok, "dark", "night") }

// add folds the rules of one sheet into the statistics. Variables are
// collected from every sheet first (collectVars), so add sees them.
func (st *styleStats) collectVars(s *sheet) {
	for _, r := range s.rules {
		if r.dark || isDarkSel(selTokens(r.sel)) {
			continue
		}
		root := isRootSel(r.sel)
		for _, d := range r.decls {
			if !strings.HasPrefix(d.prop, "--") {
				continue
			}
			if _, seen := st.vars[d.prop]; !seen {
				st.varOrder = append(st.varOrder, d.prop)
				st.vars[d.prop] = d.value
			} else if root {
				st.vars[d.prop] = d.value
			}
		}
	}
}

func (st *styleStats) resolve(v string) string {
	for range maxVarResolve {
		if !strings.Contains(v, "var(") {
			return v
		}
		v = varRe.ReplaceAllStringFunc(v, func(m string) string {
			sm := varRe.FindStringSubmatch(m)
			if val, ok := st.vars[sm[1]]; ok {
				return val
			}
			return strings.TrimSpace(sm[2])
		})
	}
	return v
}

func (st *styleStats) add(s *sheet) {
	st.darkMedia = st.darkMedia || s.darkMedia
	st.truncated = st.truncated || s.truncated
	for _, f := range s.fontFaces {
		st.fontFaces[strings.ToLower(f)] = true
	}
	for _, r := range s.rules {
		tok := selTokens(r.sel)
		if r.dark || isDarkSel(tok) {
			st.darkMedia = true
			continue
		}
		st.ruleCount++
		for _, d := range r.decls {
			if strings.HasPrefix(d.prop, "--") {
				continue
			}
			st.decl(r.sel, tok, d.prop, st.resolve(d.value))
		}
	}
}

func (st *styleStats) color(v string, f func(*colorStat)) {
	for _, c := range colorsIn(v) {
		cs := st.colors[c]
		if cs == nil {
			cs = &colorStat{c: c}
			st.colors[c] = cs
		}
		f(cs)
	}
}

func (st *styleStats) decl(sel string, tok map[string]bool, prop, v string) {
	root := isRootSel(sel)
	accent := hasAny(tok, "btn", "button", "cta", "a", "link", "primary", "accent", "badge", "tag")
	switch {
	case prop == "background" || prop == "background-color":
		st.color(v, func(c *colorStat) {
			c.bg++
			if root {
				c.rootBg++
			}
			if accent {
				c.accent++
			}
		})
		if strings.Contains(strings.ToLower(v), "url(") && hasAny(tok, "hero", "banner", "masthead", "jumbotron", "cover", "splash") {
			st.heroBgImage = true
		}
	case prop == "background-image":
		if strings.Contains(strings.ToLower(v), "url(") && hasAny(tok, "hero", "banner", "masthead", "jumbotron", "cover", "splash") {
			st.heroBgImage = true
		}
	case prop == "color":
		st.color(v, func(c *colorStat) {
			c.text++
			if root {
				c.rootText++
			}
			if accent && !root {
				c.accent++
			}
		})
	case strings.HasPrefix(prop, "border") && !strings.Contains(prop, "radius"), strings.HasPrefix(prop, "outline"):
		st.color(v, func(c *colorStat) { c.border++ })
	case strings.HasPrefix(prop, "border"):
		st.addRadius(v)
	case prop == "accent-color" || prop == "fill" || prop == "stroke" || prop == "text-decoration-color":
		st.color(v, func(c *colorStat) { c.other++ })
	case prop == "font-family":
		st.addFonts(families(v), tok, root)
	case prop == "font":
		if m := fontRe.FindStringSubmatch(v); m != nil {
			st.addSize(m[1] + m[2])
			st.addFonts(families(m[4]), tok, root)
		}
	case prop == "font-size":
		st.addSize(v)
	case strings.HasPrefix(prop, "padding") || strings.HasPrefix(prop, "margin") ||
		prop == "gap" || prop == "row-gap" || prop == "column-gap":
		for _, n := range lengths(v) {
			if n > 0 && n <= maxSpacingPx && len(st.spacing) < maxLengths {
				st.spacing = append(st.spacing, n)
			}
		}
	case prop == "box-shadow":
		if l := strings.ToLower(v); l != "none" && l != "unset" && l != "initial" && l != "0" {
			st.shadows++
			for _, n := range lengths(v) {
				st.maxShadow = max(st.maxShadow, abs(n))
			}
		}
	case prop == "position":
		if (v == "sticky" || v == "fixed") && hasAny(tok, "header", "nav", "navbar", "topbar", "masthead", "menu", "navigation") {
			st.sticky = true
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func (st *styleStats) addRadius(v string) {
	l := strings.ToLower(v)
	if strings.Contains(l, "50%") || strings.Contains(l, "100%") {
		st.pill++
		return
	}
	for _, n := range lengths(v) {
		if n >= pillPx {
			st.pill++
			continue
		}
		if len(st.radius) < maxLengths {
			st.radius = append(st.radius, n)
		}
	}
	if l == "0" {
		st.radius = append(st.radius, 0)
	}
}

func (st *styleStats) addSize(v string) {
	ls := lengths(v)
	if len(ls) == 0 {
		return
	}
	px := ls[0]
	if strings.Contains(strings.ToLower(v), "clamp(") || strings.Contains(strings.ToLower(v), "max(") {
		for _, n := range ls {
			px = max(px, n)
		}
	}
	px = float64(int(px + 0.5))
	if px >= minFontPx && px <= maxFontPx {
		st.sizes[px]++
	}
}

func (st *styleStats) addFonts(fams []string, tok map[string]bool, root bool) {
	if len(fams) == 0 {
		return
	}
	name := fams[0]
	low := strings.ToLower(name)
	for _, icon := range iconFonts {
		if strings.Contains(low, icon) {
			return
		}
	}
	if low == "inherit" || low == "initial" || low == "unset" {
		return
	}
	fs := st.fonts[name]
	if fs == nil {
		fs = &fontStat{order: len(st.fonts)}
		st.fonts[name] = fs
	}
	switch {
	case hasAny(tok, "h1", "h2", "h3", "h4", "h5", "h6", "heading", "headline", "title", "display"):
		fs.heading++
	case root || hasAny(tok, "p", "body", "text", "content"):
		fs.body++
	default:
		fs.other++
	}
}

// lengths returns the px values of the lengths in v (rem and em at 16px).
func lengths(v string) []float64 {
	var out []float64
	for _, m := range lengthRe.FindAllStringSubmatch(v, -1) {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		switch m[2] {
		case "rem", "em":
			n *= basePx
		case "pt":
			n = n * 4 / 3
		}
		out = append(out, n)
	}
	return out
}
