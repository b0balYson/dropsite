package designref

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Bounds of page parsing.
const (
	maxStyleAttrs  = 500
	maxInlineBytes = 1 << 20
	maxLinks       = 64
)

// Page is a parsed reference page.
type Page struct {
	// URL is the address of the page after redirects.
	URL *url.URL
	// Stylesheets are the absolute addresses of the linked stylesheets, in
	// document order (same-site or not).
	Stylesheets []string

	doc         *html.Node
	base        *url.URL
	title       string
	themeColor  string
	metaDark    bool
	googleFonts []string
	inline      []string
}

// ParsePage parses an HTML page fetched from pageURL.
func ParsePage(pageURL string, data []byte) (*Page, error) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid page address")
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse page: %w", err)
	}
	p := &Page{URL: u, doc: doc}
	base := u
	inlineBytes, styleAttrs := 0, 0
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Base:
				if b, err := u.Parse(attr(n, "href")); err == nil && attr(n, "href") != "" {
					base = b
				}
			case atom.Title:
				if p.title == "" && n.Namespace == "" {
					p.title = clip(collapse(text(n)), MaxTitle)
				}
			case atom.Meta:
				p.meta(n)
			case atom.Link:
				p.link(n, base)
			case atom.Style:
				if t := text(n); inlineBytes+len(t) <= maxInlineBytes {
					inlineBytes += len(t)
					p.inline = append(p.inline, t)
				}
			case atom.Script, atom.Template, atom.Noscript:
				return
			}
			if s := attr(n, "style"); s != "" && styleAttrs < maxStyleAttrs && inlineBytes+len(s) <= maxInlineBytes {
				styleAttrs++
				inlineBytes += len(s)
				p.inline = append(p.inline, styleSelector(n)+"{"+strings.ReplaceAll(s, "}", "")+"}")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	p.base = base
	return p, nil
}

func (p *Page) meta(n *html.Node) {
	name := strings.ToLower(attr(n, "name"))
	content := strings.TrimSpace(attr(n, "content"))
	switch name {
	case "theme-color":
		if p.themeColor == "" && !strings.Contains(strings.ToLower(attr(n, "media")), "dark") {
			p.themeColor = content
		}
	case "color-scheme":
		p.metaDark = strings.HasPrefix(strings.ToLower(content), "dark")
	}
}

func (p *Page) link(n *html.Node, base *url.URL) {
	rel := " " + strings.ToLower(attr(n, "rel")) + " "
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" || !strings.Contains(rel, " stylesheet ") || strings.Contains(rel, " alternate ") {
		return
	}
	u, err := base.Parse(href)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	if strings.EqualFold(u.Hostname(), "fonts.googleapis.com") {
		p.googleFonts = append(p.googleFonts, googleFamilies(u)...)
		return
	}
	if len(p.Stylesheets) < maxLinks {
		p.Stylesheets = append(p.Stylesheets, u.String())
	}
}

// SameOrigin reports whether raw has the scheme and host of the page.
func (p *Page) SameOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == p.URL.Scheme && strings.EqualFold(u.Host, p.URL.Host)
}

// googleFamilies reads the family names of a Google Fonts stylesheet link.
func googleFamilies(u *url.URL) []string {
	var out []string
	for _, f := range u.Query()["family"] {
		for _, part := range strings.Split(f, "|") {
			name, _, _ := strings.Cut(part, ":")
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// styleSelector is an internal selector for a style attribute: the tag and
// its classes, so selector heuristics work. It never leaves the package.
func styleSelector(n *html.Node) string {
	sel := n.Data
	for _, c := range strings.Fields(attr(n, "class")) {
		sel += "." + c
	}
	return strings.Map(func(r rune) rune {
		if r == '{' || r == '}' || r == ';' {
			return -1
		}
		return r
	}, sel)
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
