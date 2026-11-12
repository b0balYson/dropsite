package optimize

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// Fallback file names at the site root.
const (
	RobotsFile  = "robots.txt"
	SitemapFile = "sitemap.xml"
	FaviconFile = "favicon.ico"
)

const (
	// maxSitemapURLs is the protocol limit of one sitemap file.
	maxSitemapURLs = 50_000
	// maxHeadBytes bounds how much of a page is read to find its robots meta.
	maxHeadBytes = 256 << 10
	faviconSize  = 32
)

// StaticPath is the generated path of a fallback file.
func StaticPath(name string) string { return StaticDir + "/" + name }

func (r *runner) static(t tree) {
	if !t.has[FaviconFile] {
		r.generate(FaviconFile, func() ([]byte, error) { return Favicon(r.o.SiteID) })
	}
	if r.o.Noindex || r.o.SiteURL == "" {
		return
	}
	hasSitemap := t.has[SitemapFile]
	if !hasSitemap {
		urls := r.pageURLs(t.pages)
		if len(urls) > 0 {
			hasSitemap = r.generate(SitemapFile, func() ([]byte, error) { return Sitemap(urls) })
		}
	}
	if !t.has[RobotsFile] {
		sitemap := ""
		if hasSitemap {
			sitemap = r.o.SiteURL + "/" + SitemapFile
		}
		r.generate(RobotsFile, func() ([]byte, error) { return Robots(sitemap), nil })
	}
}

// generate writes one fallback file and reports success.
func (r *runner) generate(name string, build func() ([]byte, error)) bool {
	data, err := build()
	if err == nil {
		err = r.write(StaticPath(name), data)
	}
	if err != nil {
		r.fail("generate "+name, name, err)
		return false
	}
	r.res.Generated = append(r.res.Generated, name)
	return true
}

// pageURLs maps indexable pages to absolute addresses.
func (r *runner) pageURLs(pages []string) []string {
	var out []string
	for _, p := range pages {
		if len(out) >= maxSitemapURLs || r.ctx.Err() != nil {
			break
		}
		base := path.Base(p)
		if !strings.Contains(p, "/") && (base == "404.html" || base == "200.html") {
			continue
		}
		if hiddenPath(p) || r.pageNoindex(p) {
			continue
		}
		out = append(out, r.o.SiteURL+PageURLPath(p))
	}
	return out
}

// hiddenPath reports whether a path has a segment starting with a dot: such
// pages are not served (or are not pages, as in .well-known).
func hiddenPath(p string) bool {
	return strings.HasPrefix(p, ".") || strings.Contains(p, "/.")
}

// PageURLPath turns a release page path into the URL path that serves it:
// index.html becomes its directory with a trailing slash.
func PageURLPath(p string) string {
	u := "/" + p
	if path.Base(p) == "index.html" {
		u = strings.TrimSuffix(u, "index.html")
	}
	return (&url.URL{Path: u}).EscapedPath()
}

// pageNoindex reports whether the page asks robots not to index it.
func (r *runner) pageNoindex(p string) bool {
	f, err := r.root.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return HasNoindexMeta(io.LimitReader(f, maxHeadBytes))
}

// HasNoindexMeta reports whether an HTML document has a robots meta tag
// containing noindex before its body.
func HasNoindexMeta(rd io.Reader) bool {
	z := html.NewTokenizer(rd)
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "body":
				return false
			case "meta":
				if hasAttr && metaNoindex(z) {
					return true
				}
			}
		}
	}
}

func metaNoindex(z *html.Tokenizer) bool {
	var name, content string
	for {
		k, v, more := z.TagAttr()
		switch string(k) {
		case "name":
			name = strings.ToLower(strings.TrimSpace(string(v)))
		case "content":
			content = strings.ToLower(string(v))
		}
		if !more {
			break
		}
	}
	return (name == "robots" || name == "googlebot") && strings.Contains(content, "noindex")
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

type urlSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

// Sitemap renders a sitemap of absolute URLs.
func Sitemap(urls []string) ([]byte, error) {
	set := urlSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, u := range urls {
		set.URLs = append(set.URLs, sitemapURL{Loc: u})
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// Robots renders a robots.txt allowing everything, pointing at sitemap when
// it is not empty.
func Robots(sitemap string) []byte {
	s := "User-agent: *\nAllow: /\n"
	if sitemap != "" {
		s += "\nSitemap: " + sitemap + "\n"
	}
	return []byte(s)
}

// Favicon renders a 32x32 ICO with a filled circle whose colour is derived
// from seed, so different sites get different icons.
func Favicon(seed string) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, faviconSize, faviconSize))
	c := seedColor(seed)
	const r = faviconSize / 2
	for y := range faviconSize {
		for x := range faviconSize {
			dx, dy := x-r, y-r
			// Half-pixel offset keeps the circle symmetric.
			if 4*(dx*dx+dy*dy)+4*(dx+dy)+2 <= 4*r*r {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		return nil, err
	}
	return wrapICO(pngData.Bytes()), nil
}

func seedColor(seed string) color.NRGBA {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	v := h.Sum32()
	// Mid-tone colours: every channel in [48, 175].
	ch := func(shift uint) uint8 { return uint8(48 + (v>>shift)%128) }
	return color.NRGBA{R: ch(0), G: ch(8), B: ch(16), A: 255}
}

// wrapICO puts one PNG image into an ICO container.
func wrapICO(pngData []byte) []byte {
	const headerLen, entryLen = 6, 16
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, [3]uint16{0, 1, 1}) // reserved, type icon, one image
	b.WriteByte(faviconSize)                     // width
	b.WriteByte(faviconSize)                     // height
	b.WriteByte(0)                               // no palette
	b.WriteByte(0)                               // reserved
	_ = binary.Write(&b, le, uint16(1))          // colour planes
	_ = binary.Write(&b, le, uint16(32))         // bits per pixel
	_ = binary.Write(&b, le, uint32(len(pngData)))
	_ = binary.Write(&b, le, uint32(headerLen+entryLen))
	b.Write(pngData)
	return b.Bytes()
}
