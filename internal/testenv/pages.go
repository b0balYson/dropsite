package testenv

import (
	"context"
	"slices"
	"sync"

	"github.com/b0balYson/dropsite/internal/sitefetch"
)

// ReferenceURL is the reference page StaticPages serves by default.
const ReferenceURL = "https://reference.example/"

// ReferenceHTML is the default reference page: a dark page with a hero
// headline, cards and a script that must never leak into a brief.
const ReferenceHTML = `<!doctype html><html lang="en"><head><title>Reference</title>
<link rel="stylesheet" href="/site.css"><script>var leak = "SCRIPT-LEAK"</script></head>
<body><header class="top">Logo</header>
<section class="hero"><h1>HEADLINE-LEAK</h1><p>Intro</p></section>
<section><h2>Cards</h2><div><div class="c"><h3>A</h3></div><div class="c"><h3>B</h3></div><div class="c"><h3>C</h3></div></div></section>
<footer>Footer</footer></body></html>`

// ReferenceCSS is the stylesheet of ReferenceHTML.
const ReferenceCSS = `:root{--brand:#e4572e} body{background:#0b132b;color:#f5f5f5;font-family:"Work Sans",sans-serif}
.top{position:sticky;top:0} .c{background:#1c2541;border-radius:16px;padding:32px} a,.btn{color:var(--brand)}`

// StaticPages is a fake reference downloader serving fixed documents.
type StaticPages struct {
	mu    sync.Mutex
	Docs  map[string]sitefetch.Document
	Calls []string
}

// NewStaticPages serves ReferenceHTML at ReferenceURL with its stylesheet.
func NewStaticPages() *StaticPages {
	return &StaticPages{Docs: map[string]sitefetch.Document{
		ReferenceURL:              {ContentType: "text/html", Data: []byte(ReferenceHTML)},
		ReferenceURL + "site.css": {ContentType: "text/css", Data: []byte(ReferenceCSS)},
	}}
}

// Get implements service.PageFetcher.
func (p *StaticPages) Get(_ context.Context, raw string, o sitefetch.GetOptions) (sitefetch.Document, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, raw)
	d, ok := p.Docs[raw]
	if !ok {
		return sitefetch.Document{}, &sitefetch.Error{URL: raw, Reason: "the server answered HTTP 404"}
	}
	if !slices.Contains(o.Types, d.ContentType) {
		return sitefetch.Document{}, &sitefetch.Error{URL: raw, Reason: `content type "` + d.ContentType + `" is not accepted here`}
	}
	d.URL = raw
	return d, nil
}
