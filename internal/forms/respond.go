package forms

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// maxNextLen bounds the redirect target taken from _next.
const maxNextLen = 2048

// SafeNext returns next if it is a path on the same site ("/thanks",
// "/a?b#c") and "" otherwise. Scheme-relative ("//host"), backslash
// ("/\host"), absolute URLs and control characters are rejected, so the
// redirect can never leave the site.
func SafeNext(next string) string {
	if next == "" || len(next) > maxNextLen || next[0] != '/' {
		return ""
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return ""
	}
	for _, r := range next {
		if unicode.IsControl(r) || r == '\\' || unicode.IsSpace(r) {
			return ""
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	return next
}

// WantsHTML reports whether the request is a browser navigation (a plain
// HTML form post) rather than fetch/XHR.
func WantsHTML(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
