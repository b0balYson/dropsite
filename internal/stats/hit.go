// Package stats turns Caddy's JSON access log of published sites into page
// views: it parses lines, filters bots and assets, hashes visitors with a
// daily salt and reads the log file incrementally across rotations. It knows
// nothing about the database; internal/service persists what it produces.
package stats

import (
	"encoding/json"
	"errors"
	"math"
	"mime"
	"net"
	"net/url"
	"path"
	"strings"
	"time"
)

// MaxPathBytes bounds a stored page path; longer paths count as "other".
const MaxPathBytes = 512

// Other is the page path and referrer host under which views beyond the
// per-day row limits (and over-long paths) are counted.
const Other = "*"

// Hit is the part of one access-log line that statistics need.
type Hit struct {
	Time        time.Time
	Host        string // lower case, without port
	Method      string
	URI         string // as requested, including the query
	Status      int
	ContentType string // empty when the response had none (e.g. 304)
	UserAgent   string
	Referer     string
	ClientIP    string
}

// ErrNotAccessLog marks a well-formed JSON line that is not a request entry.
var ErrNotAccessLog = errors.New("stats: not an access log entry")

type rawLine struct {
	TS      float64 `json:"ts"`
	Request struct {
		ClientIP string              `json:"client_ip"`
		RemoteIP string              `json:"remote_ip"`
		Method   string              `json:"method"`
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
	Status      int                 `json:"status"`
	RespHeaders map[string][]string `json:"resp_headers"`
}

func first(h map[string][]string, key string) string {
	if v := h[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// ParseLine decodes one line written by Caddy's JSON access logger.
func ParseLine(line []byte) (Hit, error) {
	var r rawLine
	if err := json.Unmarshal(line, &r); err != nil {
		return Hit{}, err //nolint:wrapcheck // callers only count failures
	}
	if r.TS <= 0 || r.Request.Method == "" || r.Request.URI == "" || r.Status == 0 {
		return Hit{}, ErrNotAccessLog
	}
	sec, frac := math.Modf(r.TS)
	ip := r.Request.ClientIP
	if ip == "" {
		ip = r.Request.RemoteIP
	}
	return Hit{
		Time:        time.Unix(int64(sec), int64(frac*1e9)).UTC(),
		Host:        normalizeHost(r.Request.Host),
		Method:      r.Request.Method,
		URI:         r.Request.URI,
		Status:      r.Status,
		ContentType: first(r.RespHeaders, "Content-Type"),
		UserAgent:   first(r.Request.Headers, "User-Agent"),
		Referer:     first(r.Request.Headers, "Referer"),
		ClientIP:    ip,
	}, nil
}

// normalizeHost lower-cases a Host header and drops the port and a trailing dot.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// Day is the UTC calendar day of the hit as YYYY-MM-DD.
func (h Hit) Day() string { return h.Time.Format(time.DateOnly) }

// IsPageView reports whether the hit is a view of an HTML page: GET with
// status 200 or 304 (HTML is served with no-cache, so repeat visits are
// revalidated and answered 304), an HTML-like path and, when the response
// declares one, an HTML content type. Bots are not page views.
func (h Hit) IsPageView() bool {
	if h.Method != "GET" || (h.Status != 200 && h.Status != 304) {
		return false
	}
	if IsBot(h.UserAgent) {
		return false
	}
	if h.ContentType != "" {
		mt, _, err := mime.ParseMediaType(h.ContentType)
		if err != nil || mt != "text/html" {
			return false
		}
	}
	p := h.Path()
	if isPlatformPath(p) {
		return false
	}
	return isHTMLPath(p)
}

// isPlatformPath reports paths answered by the platform rather than the site
// (form posts, the Altcha challenge and widget, protected-site login).
func isPlatformPath(p string) bool {
	for _, prefix := range []string{"/_form", "/_dropsite"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// Path is the request path without the query, or "" if it cannot be parsed.
func (h Hit) Path() string {
	p, _, _ := strings.Cut(h.URI, "?")
	p, _, _ = strings.Cut(p, "#")
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return p
}

// PagePath is the path stored for statistics: Other when it is too long or
// not valid UTF-8 text.
func (h Hit) PagePath() string {
	p := h.Path()
	if p == "" || len(p) > MaxPathBytes || !isPrintable(p) {
		return Other
	}
	return p
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r == 0xFFFD || r < 0x20 || r == 0x7F {
			return false
		}
	}
	return true
}

func isHTMLPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasSuffix(p, "/") {
		return true
	}
	ext := strings.ToLower(path.Ext(path.Base(p)))
	return ext == "" || ext == ".html" || ext == ".htm"
}

// RefererHost returns the referring host, "" for a direct visit or an
// unusable header. Callers drop self-referrals by comparing with the site's
// own hostnames.
func (h Hit) RefererHost() string {
	if h.Referer == "" {
		return ""
	}
	u, err := url.Parse(h.Referer)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := normalizeHost(u.Host)
	if host == "" || len(host) > 253 || strings.Trim(host, "abcdefghijklmnopqrstuvwxyz0123456789.-_[]:") != "" {
		return ""
	}
	return host
}
