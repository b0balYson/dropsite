package designref

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Thresholds of the tendencies.
const (
	darkBackground   = 0.2
	compactSpacingPx = 12
	airySpacingPx    = 28
	sharpRadiusPx    = 1
	subtleRadiusPx   = 6
	roundedRadiusPx  = 20
	strongShadowPx   = 20
	littleStyleRules = 5
	rootWeight       = 5
	themeWeight      = 3
)

// Brief analyzes the page with its fetched stylesheets (CSS texts in the
// order the page links them). stats describes the stylesheets; Fetched and
// Skipped come from the caller, Inline is filled here.
func (p *Page) Brief(css []string, stats Stylesheets) Brief {
	sheets := make([]*sheet, 0, len(css)+len(p.inline))
	for _, t := range css {
		sheets = append(sheets, parseCSS(t))
	}
	for _, t := range p.inline {
		sheets = append(sheets, parseCSS(t))
	}
	st := newStyleStats()
	for _, s := range sheets {
		st.collectVars(s)
	}
	for _, s := range sheets {
		st.add(s)
	}
	if tc := colorsIn(p.themeColor); len(tc) > 0 {
		st.color(p.themeColor, func(c *colorStat) { c.accent += themeWeight })
	}
	o := p.outline()
	stats.Inline = len(p.inline)
	stats.Truncated = stats.Truncated || st.truncated || o.truncated

	b := Brief{
		URL: p.URL.String(), Title: p.title, Palette: palette(st), CustomProperties: customProps(st),
		Fonts: p.fonts(st), FontSizes: fontSizes(st), Spacing: spacing(st), Radius: radius(st),
		Shadows: shadows(st), Outline: o.sections, Stylesheets: stats, HasDarkMode: st.darkMedia,
	}
	if b.Outline == nil {
		b.Outline = []Section{}
	}
	b.ColorScheme = SchemeLight
	for _, c := range b.Palette {
		if c.Role == RoleBackground {
			if lum := mustRGB(c.Hex).luminance(); lum < darkBackground {
				b.ColorScheme = SchemeDark
			}
		}
	}
	if p.metaDark && !slices.ContainsFunc(b.Palette, func(c Color) bool { return c.Role == RoleBackground }) {
		b.ColorScheme = SchemeDark
	}
	if st.sticky {
		o.components["sticky_header"] = true
	}
	if k := heroKind(o.hero, st.heroBgImage); k != "" {
		o.components[k] = true
	}
	b.Components = []string{}
	for _, c := range componentOrder {
		if o.components[c] {
			b.Components = append(b.Components, c)
		}
	}
	b.Summary = summary(b, st.ruleCount)
	return b
}

func mustRGB(hex string) rgb {
	c, _ := parseHex(strings.TrimPrefix(hex, "#"))
	return c
}

// palette assigns roles to the most used colors.
func palette(st *styleStats) []Color {
	all := make([]*colorStat, 0, len(st.colors))
	for _, c := range st.colors {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].total() != all[j].total() {
			return all[i].total() > all[j].total()
		}
		return all[i].c.hex() < all[j].c.hex()
	})
	used := map[rgb]bool{}
	var out []Color
	pick := func(role string, score func(*colorStat) int, ok func(*colorStat) bool) bool {
		var best *colorStat
		bestScore := 0
		for _, c := range all {
			if used[c.c] || (ok != nil && !ok(c)) {
				continue
			}
			if s := score(c); s > bestScore {
				best, bestScore = c, s
			}
		}
		if best != nil {
			used[best.c] = true
			out = append(out, Color{Hex: best.c.hex(), Role: role, Uses: best.total()})
		}
		return best != nil
	}
	gray := func(c *colorStat) bool { return !c.c.vivid() }
	vivid := func(c *colorStat) bool { return c.c.vivid() }
	textScore := func(c *colorStat) int { return c.rootText*rootWeight + c.text }
	pick(RoleBackground, func(c *colorStat) int { return c.rootBg*rootWeight + c.bg }, nil)
	if !pick(RoleText, textScore, gray) {
		pick(RoleText, textScore, nil)
	}
	pick(RoleAccent, func(c *colorStat) int { return c.accent*2 + c.bg + c.text + c.border + c.other }, vivid)
	pick(RoleAccent2, func(c *colorStat) int { return c.accent + c.bg + c.text + c.border + c.other }, vivid)
	pick(RoleSurface, func(c *colorStat) int { return c.bg }, gray)
	pick(RoleMuted, func(c *colorStat) int { return c.text }, gray)
	pick(RoleBorder, func(c *colorStat) int { return c.border }, nil)
	for _, c := range all {
		if len(out) >= MaxPalette {
			break
		}
		if !used[c.c] {
			used[c.c] = true
			out = append(out, Color{Hex: c.c.hex(), Role: RoleOther, Uses: c.total()})
		}
	}
	if out == nil {
		out = []Color{}
	}
	return out
}

// customProps keeps variables whose values are colors, lengths or fonts.
func customProps(st *styleStats) []CustomProp {
	out := []CustomProp{}
	for _, name := range st.varOrder {
		if len(out) >= MaxCustomProps {
			break
		}
		v := collapse(st.resolve(st.vars[name]))
		if len(name) > MaxPropName || v == "" || strings.ContainsAny(v, "<>{}") || strings.Contains(strings.ToLower(v), "url(") {
			continue
		}
		if len(colorsIn(v)) == 0 && len(lengths(v)) == 0 && !strings.Contains(strings.ToLower(name), "font") {
			continue
		}
		out = append(out, CustomProp{Name: name, Value: clip(v, MaxPropValue)})
	}
	return out
}

func (p *Page) fonts(st *styleStats) []Font {
	type entry struct {
		name string
		fs   *fontStat
	}
	var all []entry
	for n, fs := range st.fonts {
		all = append(all, entry{n, fs})
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i].fs, all[j].fs
		if ta, tb := a.heading+a.body+a.other, b.heading+b.body+b.other; ta != tb {
			return ta > tb
		}
		return a.order < b.order
	})
	web := map[string]bool{}
	for f := range st.fontFaces {
		web[f] = true
	}
	for _, f := range p.googleFonts {
		web[strings.ToLower(f)] = true
	}
	out := []Font{}
	seen := map[string]bool{}
	for _, e := range all {
		if len(out) >= MaxFonts {
			break
		}
		role := "other"
		switch {
		case e.fs.heading > e.fs.body && e.fs.heading > 0:
			role = "headings"
		case e.fs.body > 0:
			role = "body"
		}
		seen[strings.ToLower(e.name)] = true
		out = append(out, Font{Family: clip(e.name, MaxPropName), Role: role, Web: web[strings.ToLower(e.name)]})
	}
	for _, f := range p.googleFonts {
		if len(out) < MaxFonts && !seen[strings.ToLower(f)] {
			seen[strings.ToLower(f)] = true
			out = append(out, Font{Family: clip(f, MaxPropName), Role: "other", Web: true})
		}
	}
	return out
}

func px(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) + "px" }

func fontSizes(st *styleStats) []string {
	type kv struct {
		px  float64
		cnt int
	}
	var all []kv
	for p, c := range st.sizes {
		all = append(all, kv{p, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].cnt != all[j].cnt {
			return all[i].cnt > all[j].cnt
		}
		return all[i].px > all[j].px
	})
	if len(all) > MaxFontSizes {
		all = all[:MaxFontSizes]
	}
	sort.Slice(all, func(i, j int) bool { return all[i].px > all[j].px })
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, px(e.px))
	}
	return out
}

// common returns the most frequent values, rounded to whole pixels.
func common(vals []float64) []string {
	cnt := map[float64]int{}
	for _, v := range vals {
		cnt[float64(int(v+0.5))]++
	}
	keys := make([]float64, 0, len(cnt))
	for k := range cnt {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if cnt[keys[i]] != cnt[keys[j]] {
			return cnt[keys[i]] > cnt[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > maxCommonValues {
		keys = keys[:maxCommonValues]
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, px(k))
	}
	return out
}

func median(vals []float64) float64 {
	s := slices.Clone(vals)
	slices.Sort(s)
	return s[len(s)/2]
}

func spacing(st *styleStats) Tendency {
	t := Tendency{Values: common(st.spacing), Tendency: "regular", CountUses: len(st.spacing)}
	if len(st.spacing) == 0 {
		return t
	}
	t.MedianPx = median(st.spacing)
	switch {
	case t.MedianPx < compactSpacingPx:
		t.Tendency = "compact"
	case t.MedianPx > airySpacingPx:
		t.Tendency = "airy"
	}
	return t
}

func radius(st *styleStats) Tendency {
	n := len(st.radius) + st.pill
	t := Tendency{Values: common(st.radius), Tendency: "sharp", CountUses: n}
	if n == 0 {
		return t
	}
	if st.pill*2 > n {
		t.Tendency = "pill"
	}
	if len(st.radius) == 0 {
		t.Tendency = "pill"
		return t
	}
	t.MedianPx = median(st.radius)
	if t.Tendency == "pill" {
		return t
	}
	switch {
	case t.MedianPx <= sharpRadiusPx:
		t.Tendency = "sharp"
	case t.MedianPx <= subtleRadiusPx:
		t.Tendency = "subtle"
	case t.MedianPx <= roundedRadiusPx:
		t.Tendency = "rounded"
	default:
		t.Tendency = "pill"
	}
	return t
}

func shadows(st *styleStats) string {
	switch {
	case st.shadows == 0:
		return "none"
	case st.maxShadow >= strongShadowPx:
		return "strong"
	}
	return "subtle"
}

// summary words the brief for the assistant.
func summary(b Brief, rules int) string {
	var parts []string
	roles := map[string]string{}
	for _, c := range b.Palette {
		if _, ok := roles[c.Role]; !ok {
			roles[c.Role] = c.Hex
		}
	}
	colors := []string{}
	for _, r := range []string{RoleBackground, RoleText, RoleAccent} {
		if h, ok := roles[r]; ok {
			colors = append(colors, r+" "+h)
		}
	}
	scheme := "Light"
	if b.ColorScheme == SchemeDark {
		scheme = "Dark"
	}
	line := scheme + " page"
	if len(colors) > 0 {
		line += ": " + strings.Join(colors, ", ")
	}
	if b.HasDarkMode {
		line += "; has a dark theme too"
	}
	parts = append(parts, line+".")
	var fonts []string
	for _, f := range b.Fonts {
		if f.Role != "other" {
			fonts = append(fonts, fmt.Sprintf("%s for %s", f.Family, f.Role))
		}
	}
	if len(fonts) > 0 {
		parts = append(parts, "Type: "+strings.Join(fonts, ", ")+".")
	}
	parts = append(parts, fmt.Sprintf("%s spacing, %s corners, %s shadows.",
		capital(b.Spacing.Tendency), b.Radius.Tendency, b.Shadows))
	var kinds []string
	for _, s := range b.Outline {
		if len(kinds) == 0 || kinds[len(kinds)-1] != s.Kind {
			kinds = append(kinds, s.Kind)
		}
	}
	if len(kinds) > 0 {
		parts = append(parts, "Layout: "+strings.Join(kinds, ", ")+".")
	}
	if len(b.Components) > 0 {
		parts = append(parts, "Components: "+strings.Join(b.Components, ", ")+".")
	}
	if rules < littleStyleRules {
		parts = append(parts, "Little styling was found: the page may be drawn by scripts; ask the user for a screenshot.")
	}
	return strings.Join(parts, " ")
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
