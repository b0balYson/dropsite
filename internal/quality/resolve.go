package quality

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

// refKind is what a reference turned out to be.
type refKind int

const (
	refSkip     refKind = iota // anchor, mailto:, data:, platform path...
	refLocal                   // a path inside the site
	refExternal                // another server
)

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// classify resolves a reference found in file base. For local references it
// returns the release path (no leading slash, "" for the root) and whether
// it ends with a slash; for external ones the lower-case host.
func (a *analyzer) classify(raw, base string) (kind refKind, p string, dir bool, host string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return refSkip, "", false, ""
	}
	abs := strings.HasPrefix(raw, "//")
	if m := schemeRe.FindString(raw); m != "" {
		s := strings.ToLower(m)
		if s != "http:" && s != "https:" {
			return refSkip, "", false, ""
		}
		abs = true
	}
	if abs {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return refSkip, "", false, ""
		}
		h := strings.ToLower(u.Hostname())
		if !a.hosts[h] {
			return refExternal, "", false, h
		}
		raw = u.EscapedPath()
		if raw == "" {
			raw = "/"
		}
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if raw == "" {
		// "?x" or "#x" only: the same page.
		return refSkip, "", false, ""
	}
	if dec, err := url.PathUnescape(raw); err == nil {
		raw = dec
	}
	dir = strings.HasSuffix(raw, "/")
	if strings.HasPrefix(raw, "/") {
		p = path.Clean(raw)
	} else {
		// A rooted Clean drops ".." above the root, as browsers do.
		p = path.Join("/", path.Dir(base), raw)
	}
	if isPlatformPath(p) {
		return refSkip, "", false, ""
	}
	return refLocal, strings.TrimPrefix(p, "/"), dir, ""
}

// isPlatformPath reports the paths the platform serves on every site.
func isPlatformPath(p string) bool {
	for _, r := range []string{"/_form", "/_dropsite"} {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// resolve finds the file a local path serves, the way Caddy does: the file
// itself, <path>.html, <path>/index.html.
func (a *analyzer) resolve(p string, dir bool) (string, bool) {
	var candidates []string
	switch {
	case p == "" || p == ".":
		candidates = []string{"index.html"}
	case dir:
		candidates = []string{p + "/index.html"}
	default:
		candidates = []string{p, p + ".html", p + "/index.html"}
	}
	for _, c := range candidates {
		if _, ok := a.files[c]; ok {
			return c, true
		}
	}
	return "", false
}

// srcsetURLs returns the addresses of a srcset attribute.
func srcsetURLs(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if f := strings.Fields(part); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

// kindByExt guesses a resource kind from its extension.
func kindByExt(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".woff", ".woff2", ".ttf", ".otf", ".eot":
		return "font"
	case ".css":
		return "stylesheet"
	case ".js", ".mjs":
		return "script"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico", ".bmp":
		return "image"
	case ".mp4", ".webm", ".ogg", ".mp3", ".wav", ".m4a", ".vtt":
		return "media"
	}
	return "other"
}
