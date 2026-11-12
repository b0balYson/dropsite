package quality

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// pageState collects what one page needs.
type pageState struct {
	path        string
	title       bool
	description bool
	viewport    bool
	lang        bool
	assets      map[string]bool // local files the page loads
}

// page analyzes one HTML page.
func (a *analyzer) page(p string) error {
	rc, cut, err := a.open(p, MaxPageBytes)
	if err != nil {
		return fmt.Errorf("open %s: %w", p, err)
	}
	defer func() { _ = rc.Close() }()
	if cut {
		a.rep.Truncated = true
	}
	st := &pageState{path: p, assets: map[string]bool{}}
	z := html.NewTokenizer(rc)
	inTitle, inStyle := false, false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			a.finishPage(st)
			return nil
		case html.TextToken:
			switch {
			case inTitle && strings.TrimSpace(string(z.Text())) != "":
				st.title = true
			case inStyle:
				a.cssRefs(string(z.Text()), p, st.assets)
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "style":
				inStyle = false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if _, dup := attrs[string(k)]; !dup {
					attrs[string(k)] = string(v)
				}
			}
			switch tag {
			case "title":
				inTitle = tt == html.StartTagToken
			case "style":
				inStyle = tt == html.StartTagToken
			}
			a.element(st, tag, attrs)
		}
	}
}

// element handles one start tag.
func (a *analyzer) element(st *pageState, tag string, attrs map[string]string) {
	if style, ok := attrs["style"]; ok {
		a.cssRefs(style, st.path, st.assets)
	}
	switch tag {
	case "html":
		st.lang = strings.TrimSpace(attrs["lang"]) != ""
	case "meta":
		a.meta(st, attrs)
	case "a", "area":
		a.link(st, attrs["href"])
	case "img":
		if _, ok := attrs["alt"]; !ok {
			a.issue(&a.rep.ImagesWithoutAlt, &a.rep.Counts.ImagesWithoutAlt,
				Issue{Page: st.path, Target: attrs["src"]}, "alt")
		}
		a.resource(st, "image", attrs["src"])
		a.srcset(st, "image", attrs["srcset"])
	case "source":
		a.resource(st, "", attrs["src"])
		a.srcset(st, "image", attrs["srcset"])
	case "video", "audio":
		a.resource(st, "media", attrs["src"])
		a.resource(st, "image", attrs["poster"])
	case "track", "embed":
		a.resource(st, "media", attrs["src"])
	case "script":
		a.resource(st, "script", attrs["src"])
	case "link":
		if kind := linkKind(attrs); kind != "" {
			a.resource(st, kind, attrs["href"])
		}
	}
}

func (a *analyzer) meta(st *pageState, attrs map[string]string) {
	switch strings.ToLower(strings.TrimSpace(attrs["name"])) {
	case "description":
		if strings.TrimSpace(attrs["content"]) != "" {
			st.description = true
		}
	case "viewport":
		st.viewport = true
	}
}

// linkKind returns the kind of a <link> the browser loads, "" for links it
// does not load (canonical, alternate, ...).
func linkKind(attrs map[string]string) string {
	for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
		switch rel {
		case "stylesheet":
			return "stylesheet"
		case "icon", "apple-touch-icon", "mask-icon":
			return "icon"
		case "modulepreload":
			return "script"
		case "manifest":
			return "other"
		case "preload", "prefetch":
			switch strings.ToLower(attrs["as"]) {
			case "font":
				return "font"
			case "style":
				return "stylesheet"
			case "script":
				return "script"
			case "image":
				return "image"
			}
			return "other"
		}
	}
	return ""
}

// link checks a navigation link: only local pages are checked, and none in
// SPA mode where the app routes unknown paths itself.
func (a *analyzer) link(st *pageState, href string) {
	kind, p, dir, _ := a.classify(href, st.path)
	if kind != refLocal || a.spa {
		return
	}
	if _, ok := a.resolve(p, dir); !ok {
		a.issue(&a.rep.BrokenLinks, &a.rep.Counts.BrokenLinks,
			Issue{Page: st.path, Target: strings.TrimSpace(href)}, "link")
	}
}

func (a *analyzer) srcset(st *pageState, kind, v string) {
	for _, u := range srcsetURLs(v) {
		a.resource(st, kind, u)
	}
}

// resource checks a file the page loads automatically.
func (a *analyzer) resource(st *pageState, kind, raw string) {
	a.ref(st.path, kind, raw, st.assets)
}

// ref checks one reference made from file base (a page or a stylesheet)
// and adds the local file it loads to assets.
func (a *analyzer) ref(base, kind, raw string, assets map[string]bool) {
	k, p, dir, host := a.classify(raw, base)
	if kind == "" {
		kind = kindByExt(strings.SplitN(raw, "?", 2)[0])
	}
	switch k {
	case refExternal:
		a.external(base, kind, strings.TrimSpace(raw), host)
	case refLocal:
		f, ok := a.resolve(p, dir)
		if !ok {
			a.issue(&a.rep.MissingAssets, &a.rep.Counts.MissingAssets,
				Issue{Page: base, Target: strings.TrimSpace(raw), Kind: kind}, "asset")
			return
		}
		if f != base {
			assets[f] = true
		}
	}
}

// finishPage records metadata problems and the page weight.
func (a *analyzer) finishPage(st *pageState) {
	m := &a.rep.PagesMissing
	c := &a.rep.Counts
	if !st.title {
		a.pageMissing(&m.Title, &c.MissingTitle, st.path)
	}
	if !st.description {
		a.pageMissing(&m.Description, &c.MissingDescription, st.path)
	}
	if !st.viewport {
		a.pageMissing(&m.Viewport, &c.MissingViewport, st.path)
	}
	if !st.lang {
		a.pageMissing(&m.Lang, &c.MissingLang, st.path)
	}
	a.weights = append(a.weights, PageSize{Page: st.path, Bytes: a.weight(st)})
}
