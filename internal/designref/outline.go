package designref

import (
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Bounds of the outline walk.
const (
	maxWrapDepth   = 4
	maxRepeatDepth = 5
	maxHeadings    = 12
	minRepeat      = 3
)

// Section kinds; only these words leave the package.
var kindWords = []struct {
	kind  string
	words []string
}{
	{"header", []string{"header", "topbar"}},
	{"nav", []string{"nav", "navbar", "navigation"}},
	{"footer", []string{"footer"}},
	{"hero", []string{"hero", "banner", "masthead", "jumbotron", "cover", "splash"}},
	{"gallery", []string{"gallery", "portfolio", "photos", "lightbox"}},
	{"pricing", []string{"pricing", "prices", "price", "plans", "tariffs"}},
	{"testimonials", []string{"testimonial", "testimonials", "reviews", "review", "quotes"}},
	{"faq", []string{"faq", "faqs", "accordion", "questions"}},
	{"contact", []string{"contact", "contacts"}},
	{"features", []string{"features", "feature", "benefits", "services", "advantages"}},
	{"team", []string{"team", "people", "staff"}},
	{"cta", []string{"cta", "subscribe", "newsletter", "signup"}},
	{"map", []string{"map", "location"}},
}

var wrapperWords = []string{"wrapper", "wrap", "container", "page", "app", "site", "root", "content", "layout", "main", "next"}

var carouselWords = []string{"carousel", "slider", "swiper", "slick", "glide", "splide", "slideshow"}

// componentOrder fixes the order of components in the brief.
var componentOrder = []string{
	"sticky_header", "hero_image", "hero_split", "hero_text", "hero_video", "cards", "gallery", "carousel",
	"features", "pricing", "testimonials", "team", "faq", "cta", "form", "map", "video",
}

func classTokens(n *html.Node) map[string]bool {
	return selTokens(attr(n, "class") + " " + attr(n, "id") + " " + attr(n, "role"))
}

func kindOf(n *html.Node) string {
	switch n.DataAtom {
	case atom.Header:
		return "header"
	case atom.Nav:
		return "nav"
	case atom.Footer:
		return "footer"
	case atom.Form:
		return "form"
	}
	tok := classTokens(n)
	for _, k := range kindWords {
		if hasAny(tok, k.words...) {
			return k.kind
		}
	}
	switch strings.ToLower(attr(n, "role")) {
	case "banner":
		return "header"
	case "contentinfo":
		return "footer"
	case "navigation":
		return "nav"
	}
	return ""
}

func elements(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.DataAtom {
		case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Link, atom.Meta, atom.Br, atom.Hr:
			continue
		}
		if c.Namespace != "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func find(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

func hasLandmarkChild(n *html.Node) bool {
	for _, c := range elements(n) {
		if kindOf(c) != "" || c.DataAtom == atom.Section || c.DataAtom == atom.Main {
			return true
		}
	}
	return false
}

// outline is the page structure plus what the walk noticed.
type outline struct {
	sections   []Section
	hero       *html.Node
	components map[string]bool
	truncated  bool
}

func (p *Page) outline() outline {
	o := outline{components: map[string]bool{}}
	body := find(p.doc, atom.Body)
	if body == nil {
		return o
	}
	var nodes []*html.Node
	var visit func(n *html.Node, depth int)
	visit = func(n *html.Node, depth int) {
		for _, c := range elements(n) {
			k := kindOf(c)
			wrapper := c.DataAtom == atom.Main ||
				(k == "" && c.DataAtom == atom.Div && (hasAny(classTokens(c), wrapperWords...) || hasLandmarkChild(c)))
			if wrapper && depth < maxWrapDepth && len(elements(c)) >= 2 {
				visit(c, depth+1)
				continue
			}
			nodes = append(nodes, c)
		}
	}
	visit(body, 0)
	if len(nodes) > MaxSections {
		nodes, o.truncated = nodes[:MaxSections], true
	}
	for _, n := range nodes {
		o.sections = append(o.sections, o.section(n))
	}
	o.global(body)
	return o
}

func (o *outline) section(n *html.Node) Section {
	kind := kindOf(n)
	s := Section{Kind: kind}
	walkHeadings(n, &s)
	if kind == "" {
		s.Kind = "section"
		if o.hero == nil && slices.Contains(s.Headings, "h1") {
			s.Kind = "hero"
		}
	}
	if s.Kind == "hero" && o.hero == nil {
		o.hero = n
	}
	switch s.Kind {
	case "header", "nav", "footer", "hero":
	default:
		count, images := repeated(n, 0)
		if count >= minRepeat {
			s.Repeated = count
			if s.Kind == "section" {
				s.Kind = "cards"
				if images {
					s.Kind = "gallery"
				}
			}
		}
	}
	if s.Kind == "header" && hasAny(classTokens(n), "sticky", "fixed") {
		o.components["sticky_header"] = true
	}
	switch s.Kind {
	case "cards", "gallery", "features", "pricing", "testimonials", "team", "faq", "cta", "map":
		o.components[s.Kind] = true
	}
	return s
}

func walkHeadings(n *html.Node, s *Section) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.DataAtom {
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			if len(s.Headings) < maxHeadings {
				s.Headings = append(s.Headings, c.Data)
			}
			continue
		case atom.Img, atom.Video:
			s.Images++
		case atom.Script, atom.Style, atom.Template:
			continue
		}
		walkHeadings(c, s)
	}
}

func contains(n *html.Node, atoms ...atom.Atom) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (slices.Contains(atoms, c.DataAtom) || contains(c, atoms...)) {
			return true
		}
	}
	return false
}

// repeated finds the largest group of similar siblings below n and whether
// its items are mostly pictures without headings.
func repeated(n *html.Node, depth int) (int, bool) {
	groups := map[string][]*html.Node{}
	for _, c := range elements(n) {
		cls := strings.Fields(attr(c, "class"))
		sort.Strings(cls)
		sig := c.Data + "." + strings.Join(cls, ".")
		groups[sig] = append(groups[sig], c)
	}
	best, images := 0, false
	for _, g := range groups {
		if len(g) > best {
			pics := 0
			for _, item := range g {
				pic := item.DataAtom == atom.Img || contains(item, atom.Img, atom.Picture)
				if pic && !contains(item, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.P) {
					pics++
				}
			}
			best, images = len(g), pics*2 > len(g)
		}
	}
	if depth < maxRepeatDepth {
		for _, c := range elements(n) {
			if cnt, img := repeated(c, depth+1); cnt > best {
				best, images = cnt, img
			}
		}
	}
	return best, images
}

// global notices components anywhere on the page.
func (o *outline) global(body *html.Node) {
	details := 0
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tok := classTokens(n)
			if hasAny(tok, carouselWords...) {
				o.components["carousel"] = true
			}
			switch n.DataAtom {
			case atom.Form:
				if !strings.EqualFold(attr(n, "role"), "search") && !hasSearchInput(n) {
					o.components["form"] = true
				}
			case atom.Video:
				o.components["video"] = true
			case atom.Iframe:
				src := strings.ToLower(attr(n, "src"))
				switch {
				case strings.Contains(src, "map"):
					o.components["map"] = true
				case strings.Contains(src, "youtube") || strings.Contains(src, "vimeo") || strings.Contains(src, "rutube"):
					o.components["video"] = true
				}
			case atom.Details:
				details++
			case atom.Script, atom.Style, atom.Template:
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)
	if details >= 2 {
		o.components["faq"] = true
	}
}

func hasSearchInput(n *html.Node) bool {
	if n.Type == html.ElementNode && n.DataAtom == atom.Input && strings.EqualFold(attr(n, "type"), "search") {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasSearchInput(c) {
			return true
		}
	}
	return false
}

// heroKind classifies the first screen.
func heroKind(hero *html.Node, cssBackground bool) string {
	switch {
	case hero == nil:
		return ""
	case contains(hero, atom.Video):
		return "hero_video"
	case cssBackground || strings.Contains(strings.ToLower(attr(hero, "style")), "url("):
		return "hero_image"
	case contains(hero, atom.Img, atom.Picture):
		return "hero_split"
	}
	return "hero_text"
}
