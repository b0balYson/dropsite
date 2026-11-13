package designref

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SnapshotIndex is the page file of a snapshot.
const SnapshotIndex = "index.html"

// snapshotCSP forbids scripts and anything outside the render listener.
const snapshotCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self' data:; script-src 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"

// Sheet is a fetched stylesheet.
type Sheet struct {
	URL string
	CSS string
}

// removed are elements dropped from a snapshot with their content.
var removed = map[atom.Atom]bool{
	atom.Script: true, atom.Noscript: true, atom.Iframe: true, atom.Object: true, atom.Embed: true,
	atom.Base: true, atom.Frame: true, atom.Frameset: true, atom.Applet: true, atom.Template: true,
}

// cleared are attributes whose value is dropped: they would load content.
var cleared = map[string]bool{
	"src": true, "srcset": true, "poster": true, "data": true, "action": true, "formaction": true,
	"ping": true, "background": true, "imagesrcset": true, "xlink:href": true,
}

// Snapshot builds a sanitized copy of the page for the screenshot renderer:
// no scripts, event handlers, frames, base or refresh, no external loads,
// and the fetched stylesheets as local files s/N.css in place of their
// links. It returns the files of the snapshot keyed by path.
func (p *Page) Snapshot(sheets []Sheet) (map[string][]byte, error) {
	var src bytes.Buffer
	if err := html.Render(&src, p.doc); err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}
	doc, err := html.Parse(&src)
	if err != nil {
		return nil, fmt.Errorf("parse page copy: %w", err)
	}
	files := map[string][]byte{}
	local := map[string]string{}
	for i, s := range sheets {
		name := fmt.Sprintf("s/%d.css", i)
		files[name] = []byte(s.CSS)
		local[s.URL] = name
	}
	linked := map[string]bool{}
	base := p.base
	sanitize(doc, func(n *html.Node) {
		href := strings.TrimSpace(attr(n, "href"))
		u, err := base.Parse(href)
		rel := " " + strings.ToLower(attr(n, "rel")) + " "
		if err == nil && strings.Contains(rel, " stylesheet ") {
			if name, ok := local[u.String()]; ok && !linked[name] {
				linked[name] = true
				n.Attr = []html.Attribute{{Key: "rel", Val: "stylesheet"}, {Key: "href", Val: name}}
				return
			}
		}
		n.Parent.RemoveChild(n)
	})
	head := find(doc, atom.Head)
	if head != nil {
		head.InsertBefore(element("meta", "http-equiv", "Content-Security-Policy", "content", snapshotCSP), head.FirstChild)
		for i := range sheets {
			if name := fmt.Sprintf("s/%d.css", i); !linked[name] {
				head.AppendChild(element("link", "rel", "stylesheet", "href", name))
			}
		}
	}
	var out bytes.Buffer
	if err := html.Render(&out, doc); err != nil {
		return nil, fmt.Errorf("render snapshot: %w", err)
	}
	files[SnapshotIndex] = out.Bytes()
	return files, nil
}

func element(tag string, kv ...string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
	for i := 0; i+1 < len(kv); i += 2 {
		n.Attr = append(n.Attr, html.Attribute{Key: kv[i], Val: kv[i+1]})
	}
	return n
}

// sanitize removes dangerous nodes and attributes; links go to onLink,
// which keeps or removes them.
func sanitize(n *html.Node, onLink func(*html.Node)) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode:
			n.RemoveChild(c)
		case c.Type != html.ElementNode:
		case removed[c.DataAtom]:
			n.RemoveChild(c)
		case c.DataAtom == atom.Meta && attr(c, "http-equiv") != "":
			n.RemoveChild(c)
		case c.DataAtom == atom.Link:
			onLink(c)
		default:
			c.Attr = cleanAttrs(c.Attr)
			sanitize(c, onLink)
		}
		c = next
	}
}

func cleanAttrs(in []html.Attribute) []html.Attribute {
	out := make([]html.Attribute, 0, len(in))
	for _, a := range in {
		key := strings.ToLower(a.Key)
		if a.Namespace != "" {
			key = strings.ToLower(a.Namespace + ":" + a.Key)
		}
		switch {
		case strings.HasPrefix(key, "on"):
			continue
		case cleared[key]:
			continue
		case key == "href" || key == "xlink:href":
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
				a.Val = "#"
			}
		}
		out = append(out, a)
	}
	return out
}
