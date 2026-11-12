package designref

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const cafePage = `<!doctype html>
<html lang="ru"><head>
<title>  Кофейня
  «Река» </title>
<meta name="theme-color" content="#d9480f">
<link rel="stylesheet" href="/css/site.css">
<link rel="stylesheet" href="https://cdn.example.net/lib.css">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Playfair+Display:wght@700&family=Inter:wght@400;600">
<link rel="icon" href="/favicon.ico">
<style>.btn{background:#d9480f;color:#fff;border-radius:999px;padding:12px 28px}</style>
<script>window.secretTracking = "SCRIPT-CODE-123"</script>
</head><body>
<div class="page-wrapper">
<header class="site-header"><nav><a href="/">Home</a><a href="/menu">Menu</a></nav></header>
<main>
<section class="intro"><h1>Лучший кофе в городе</h1><p>Обжариваем сами.</p><img src="/img/logo.png" alt="logo"></section>
<section class="menu-list"><h2>Меню</h2>
  <div class="grid">
    <article class="card"><h3>Эспрессо</h3><p>120 ₽</p></article>
    <article class="card"><h3>Капучино</h3><p>180 ₽</p></article>
    <article class="card"><h3>Латте</h3><p>200 ₽</p></article>
  </div>
</section>
<section class="photos"><h2>Фото</h2>
  <div><img src="/a.jpg"><img src="/b.jpg"><img src="/c.jpg"><img src="/d.jpg"></div>
</section>
<section id="contact"><h2>Контакты</h2><form action="/_form/contact"><input name="name"><button onclick="steal()">Send</button></form></section>
</main>
<footer><p>© Река</p></footer>
</div>
</body></html>`

const cafeCSS = `/* site */
:root { --bg: #fffaf3; --ink: #222; --accent: #d9480f; --radius: 12px; --gap: 2rem; --font-head: "Playfair Display", serif; --junk: block; }
@font-face { font-family: "Inter"; src: url(/fonts/inter.woff2) format("woff2"); }
@import url("/css/extra.css");
html, body { background: var(--bg); color: var(--ink); font-family: Inter, system-ui, sans-serif; font-size: 16px; }
h1 { font: 700 3rem/1.1 var(--font-head); color: var(--ink); }
h2, h3 { font-family: var(--font-head); font-size: clamp(1.5rem, 2vw, 2rem); }
a { color: var(--accent); }
.site-header { position: sticky; top: 0; background: #ffffff; border-bottom: 1px solid #eadfd0; }
.card { background: #ffffff; border-radius: var(--radius); padding: var(--gap); box-shadow: 0 2px 8px rgba(0,0,0,.08); }
section { padding: 64px 24px; margin: 0 auto; }
.grid { display: grid; gap: 32px; }
@media (prefers-color-scheme: dark) { html, body { background: #111; color: #eee; } }
@keyframes spin { from { color: #00ff00; } to { color: #ff00ff; } }
.overlay { background: rgba(0, 0, 0, 0.1); }
`

func cafeBrief(t *testing.T) (Brief, *Page) {
	t.Helper()
	p, err := ParsePage("https://example.com/", []byte(cafePage))
	if err != nil {
		t.Fatal(err)
	}
	return p.Brief([]string{cafeCSS}, Stylesheets{Fetched: 1, Skipped: 1}), p
}

func role(b Brief, r string) string {
	for _, c := range b.Palette {
		if c.Role == r {
			return c.Hex
		}
	}
	return ""
}

func TestBriefPalette(t *testing.T) {
	b, _ := cafeBrief(t)
	if got := role(b, RoleBackground); got != "#fffaf3" {
		t.Errorf("background %q, palette %+v", got, b.Palette)
	}
	if got := role(b, RoleText); got != "#222222" {
		t.Errorf("text %q, palette %+v", got, b.Palette)
	}
	if got := role(b, RoleAccent); got != "#d9480f" {
		t.Errorf("accent %q, palette %+v", got, b.Palette)
	}
	if role(b, RoleSurface) != "#ffffff" {
		t.Errorf("surface: %+v", b.Palette)
	}
	if b.ColorScheme != SchemeLight || !b.HasDarkMode {
		t.Errorf("scheme %s dark mode %v", b.ColorScheme, b.HasDarkMode)
	}
	for _, c := range b.Palette {
		if c.Hex == "#00ff00" || c.Hex == "#111111" || c.Hex == "#000000" {
			t.Errorf("keyframes, dark theme or transparent color in palette: %+v", b.Palette)
		}
	}
	if len(b.Palette) > MaxPalette {
		t.Errorf("palette too long: %d", len(b.Palette))
	}
}

func TestBriefTypographyAndTendencies(t *testing.T) {
	b, _ := cafeBrief(t)
	fonts := map[string]Font{}
	for _, f := range b.Fonts {
		fonts[f.Family] = f
	}
	if f := fonts["Playfair Display"]; f.Role != "headings" || !f.Web {
		t.Errorf("heading font: %+v", b.Fonts)
	}
	if f := fonts["Inter"]; f.Role != "body" || !f.Web {
		t.Errorf("body font: %+v", b.Fonts)
	}
	if len(b.FontSizes) == 0 || b.FontSizes[0] != "48px" {
		t.Errorf("font sizes %v", b.FontSizes)
	}
	if b.Spacing.Tendency != "airy" {
		t.Errorf("spacing %+v", b.Spacing)
	}
	if b.Shadows != "subtle" {
		t.Errorf("shadows %s", b.Shadows)
	}
	if role(b, RoleAccent2) != "" || role(b, RoleBorder) != "#eadfd0" {
		t.Errorf("a beige border is not an accent: %+v", b.Palette)
	}
	if b.Radius.Tendency != "rounded" {
		t.Errorf("radius %+v", b.Radius)
	}
	names := map[string]string{}
	for _, v := range b.CustomProperties {
		names[v.Name] = v.Value
	}
	if names["--accent"] != "#d9480f" || names["--radius"] != "12px" || names["--font-head"] == "" {
		t.Errorf("custom properties %+v", b.CustomProperties)
	}
	if _, ok := names["--junk"]; ok {
		t.Error("non-design variable kept")
	}
}

func TestBriefOutlineAndComponents(t *testing.T) {
	b, _ := cafeBrief(t)
	var kinds []string
	for _, s := range b.Outline {
		kinds = append(kinds, s.Kind)
	}
	want := []string{"header", "hero", "cards", "gallery", "contact", "footer"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("outline %v, want %v", kinds, want)
	}
	if h := b.Outline[2].Headings; strings.Join(h, ",") != "h2,h3,h3,h3" || b.Outline[2].Repeated != 3 {
		t.Errorf("cards section %+v", b.Outline[2])
	}
	for _, c := range []string{"sticky_header", "hero_split", "cards", "gallery", "form"} {
		if !slices.Contains(b.Components, c) {
			t.Errorf("component %s missing: %v", c, b.Components)
		}
	}
	if b.Stylesheets.Fetched != 1 || b.Stylesheets.Skipped != 1 || b.Stylesheets.Inline == 0 {
		t.Errorf("stylesheets %+v", b.Stylesheets)
	}
	if !strings.Contains(b.Summary, "Light page") || !strings.Contains(b.Summary, "#d9480f") {
		t.Errorf("summary %q", b.Summary)
	}
}

func TestBriefCarriesNoPageContent(t *testing.T) {
	b, _ := cafeBrief(t)
	if b.Title != "Кофейня «Река»" {
		t.Errorf("title %q", b.Title)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Лучший кофе", "Эспрессо", "120", "logo.png", "/img/", "a.jpg", "SCRIPT-CODE",
		"secretTracking", "steal", "page-wrapper", "site-header", "menu-list", "_form", "Контакты", "©"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("brief leaks %q: %s", leak, raw)
		}
	}
}

func TestPageLinks(t *testing.T) {
	_, p := cafeBrief(t)
	if strings.Join(p.Stylesheets, " ") != "https://example.com/css/site.css https://cdn.example.net/lib.css" {
		t.Fatalf("stylesheets %v", p.Stylesheets)
	}
	if !p.SameOrigin(p.Stylesheets[0]) || p.SameOrigin(p.Stylesheets[1]) || p.SameOrigin("http://example.com/x.css") {
		t.Error("same origin")
	}
	if got := Imports(cafeCSS, "https://example.com/css/site.css"); len(got) != 1 || got[0] != "https://example.com/css/extra.css" {
		t.Errorf("imports %v", got)
	}
}

func TestDarkPageAndEmptyPage(t *testing.T) {
	p, err := ParsePage("https://dark.example/", []byte(`<html><head><style>
body{background:hsl(220 20% 8%);color:rgb(230 230 240)} a,.btn{color:#7c3aed;background-color:#7c3aed}
</style></head><body><h1>x</h1></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	b := p.Brief(nil, Stylesheets{})
	if b.ColorScheme != SchemeDark || role(b, RoleAccent) != "#7c3aed" {
		t.Errorf("dark brief %+v", b)
	}
	p, err = ParsePage("https://spa.example/", []byte(`<html><body><div id="root"></div><script src="/app.js"></script></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	b = p.Brief(nil, Stylesheets{})
	if !strings.Contains(b.Summary, "screenshot") || b.Palette == nil || b.Outline == nil || b.Components == nil {
		t.Errorf("empty brief %+v", b)
	}
}

func TestColorParsing(t *testing.T) {
	cases := map[string]string{
		"#abc": "#aabbcc", "#AABBCC": "#aabbcc", "rgb(255, 0, 0)": "#ff0000", "rgba(0 0 255 / 80%)": "#0000ff",
		"hsl(0, 100%, 50%)": "#ff0000", "white": "#ffffff", "1px solid tomato": "#ff6347",
	}
	for in, want := range cases {
		got := colorsIn(in)
		if len(got) != 1 || got[0].hex() != want {
			t.Errorf("%s: %v, want %s", in, got, want)
		}
	}
	for _, in := range []string{"rgba(0,0,0,.1)", "#0000001a", "transparent", "url(white.png)", "var(--white)"} {
		if got := colorsIn(in); len(got) != 0 {
			t.Errorf("%s: %v, want none", in, got)
		}
	}
}

func TestCSSScannerTolerance(t *testing.T) {
	s := parseCSS(`a{color:red} .x{content:"}{";color:blue} @supports (display:grid){.g{gap:8px}} .n{ .m{ color: green } }` +
		`@media screen{ .unclosed { color: navy`)
	sels := []string{}
	for _, r := range s.rules {
		sels = append(sels, r.sel)
	}
	for _, want := range []string{"a", ".x", ".g", ".n .m"} {
		if !slices.Contains(sels, want) {
			t.Errorf("selector %q missing from %v", want, sels)
		}
	}
	if !s.truncated {
		t.Error("an unclosed block must mark the sheet truncated")
	}
}

func TestSnapshotIsSanitized(t *testing.T) {
	_, p := cafeBrief(t)
	files, err := p.Snapshot([]Sheet{
		{URL: "https://example.com/css/site.css", CSS: cafeCSS},
		{URL: "https://example.com/css/extra.css", CSS: "p{color:#333}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	page := string(files[SnapshotIndex])
	for _, bad := range []string{"<script", "SCRIPT-CODE", "onclick", "steal()", "cdn.example.net", "fonts.googleapis",
		"favicon", "/img/logo.png", `action=`} {
		if strings.Contains(page, bad) {
			t.Errorf("snapshot keeps %q:\n%s", bad, page)
		}
	}
	for _, good := range []string{`href="s/0.css"`, `href="s/1.css"`, "Content-Security-Policy", "script-src &#39;none&#39;"} {
		if !strings.Contains(page, good) {
			t.Errorf("snapshot lacks %q:\n%s", good, page)
		}
	}
	if string(files["s/0.css"]) != cafeCSS || len(files) != 3 {
		t.Errorf("files %v", len(files))
	}
	if strings.Contains(page, "Лучший кофе") == false {
		t.Error("the snapshot is the page itself for the renderer; its text stays")
	}
}
