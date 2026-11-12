// Package sitefetch downloads images, fonts, stylesheets and scripts from the
// public internet into a site on the server, on behalf of an AI assistant.
// It is an SSRF boundary: only https on port 443, only public addresses (the
// address actually connected to is checked on every connection, including
// every redirect hop), no proxy, bounded size, count and time, and an
// allowlist of content types and target extensions.
package sitefetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/b0balYson/dropsite/internal/netguard"
)

// Default limits (design mcp-site-files, decision 6).
const (
	MaxURLs          = 500
	MaxFileBytes     = 50 << 20
	MaxCallBytes     = 256 << 20
	CallTimeout      = 10 * time.Minute
	MaxRedirects     = 3
	maxURLLen        = 2048
	dialTimeout      = 5 * time.Second
	headerTimeout    = 15 * time.Second
	parallelFetches  = 4
	userAgent        = "dropsite-fetch"
	sniffBytes       = 512
	defaultHTTPSPort = "443"
)

// Request asks for url to be stored at path inside the site.
type Request struct {
	URL  string
	Path string
}

// File is a downloaded resource.
type File struct {
	URL         string
	Path        string
	Data        []byte
	ContentType string
	// OriginalBytes is the downloaded size before resizing.
	OriginalBytes int64
	Resized       bool
}

// Error explains why one URL failed; the whole call fails with it.
type Error struct {
	URL    string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("fetch %s: %s", e.URL, e.Reason) }

func fail(u, reason string) *Error { return &Error{URL: u, Reason: reason} }

// Options configure a Client. Zero values mean production defaults.
type Options struct {
	Resolver netguard.Resolver
	// Allow decides which addresses may be contacted (default: public only).
	Allow func(netip.Addr) bool
	// Port replaces port 443 when connecting (tests only).
	Port    string
	RootCAs *x509.CertPool
	Timeout time.Duration
}

// Client fetches resources.
type Client struct {
	http    *http.Client
	timeout time.Duration
}

// New builds a client.
func New(o Options) *Client {
	allow := o.Allow
	if allow == nil {
		allow = netguard.PublicAddr
	}
	nd := &net.Dialer{Timeout: dialTimeout, Control: netguard.Control(allow)}
	guard := &netguard.Dialer{Resolver: o.Resolver, OK: allow, Dialer: nd, Port: o.Port}
	tr := &http.Transport{
		Proxy: nil, // a proxy would bypass the address check
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if _, port, err := net.SplitHostPort(addr); err != nil || port != defaultHTTPSPort {
				return nil, errors.New("only the standard https port 443 is allowed")
			}
			return guard.DialContext(ctx, network, addr)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs},
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: headerTimeout,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = CallTimeout
	}
	return &Client{
		timeout: timeout,
		http: &http.Client{
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > MaxRedirects {
					return errors.New("too many redirects")
				}
				if err := checkURL(req.URL); err != nil {
					return err
				}
				return nil
			},
		},
	}
}

// ValidateURL checks a URL before anything is fetched.
func ValidateURL(raw string) error {
	if len(raw) > maxURLLen {
		return errors.New("the address is too long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("enter a full https:// address")
	}
	return checkURL(u)
}

func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return errors.New("only https:// addresses can be fetched")
	}
	if u.User != nil {
		return errors.New("addresses with a user name or password are not allowed")
	}
	if p := u.Port(); p != "" && p != defaultHTTPSPort {
		return errors.New("only the standard https port 443 is allowed")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return netguard.ErrBlocked
	}
	if ip, err := netip.ParseAddr(host); err == nil && !netguard.PublicAddr(ip) {
		return netguard.ErrBlocked
	}
	return nil
}

// Fetch downloads every request or fails as a whole. maxFile caps one file
// (it is lowered to MaxFileBytes); the call is bounded by MaxURLs,
// MaxCallBytes and the client timeout.
func (c *Client) Fetch(ctx context.Context, reqs []Request, maxFile int64) ([]File, error) {
	if len(reqs) > MaxURLs {
		return nil, fail("", fmt.Sprintf("at most %d addresses per call", MaxURLs))
	}
	if maxFile <= 0 || maxFile > MaxFileBytes {
		maxFile = MaxFileBytes
	}
	for _, r := range reqs {
		if err := ValidateURL(r.URL); err != nil {
			return nil, fail(r.URL, err.Error())
		}
		if !AllowedPath(r.Path) {
			return nil, fail(r.URL, "the target path must end with an image, font, .css or .js extension")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out := make([]File, len(reqs))
	var (
		mu    sync.Mutex
		total int64
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelFetches)
	for i, r := range reqs {
		g.Go(func() error {
			f, err := c.one(gctx, r, maxFile)
			if err != nil {
				return err
			}
			mu.Lock()
			total += int64(len(f.Data))
			over := total > MaxCallBytes
			mu.Unlock()
			if over {
				return fail(r.URL, fmt.Sprintf("the files of one call are larger than %d MB in total", MaxCallBytes>>20))
			}
			out[i] = f
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		var fe *Error
		if errors.As(err, &fe) {
			return nil, fe
		}
		return nil, fail("", err.Error())
	}
	return out, nil
}

func (c *Client) one(ctx context.Context, r Request, maxFile int64) (File, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(r.URL), nil)
	if err != nil {
		return File{}, fail(r.URL, "invalid address")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "image/*,font/*,text/css,application/javascript,*/*;q=0.1")
	resp, err := c.http.Do(req) //nolint:gosec // SSRF guarded: public addresses only, checked on every connection
	if err != nil {
		return File{}, fail(r.URL, describe(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return File{}, fail(r.URL, fmt.Sprintf("the server answered HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFile+1))
	if err != nil {
		return File{}, fail(r.URL, describe(err))
	}
	if int64(len(data)) > maxFile {
		return File{}, fail(r.URL, fmt.Sprintf("the file is larger than %d KB", maxFile>>10))
	}
	ct := contentType(resp.Header.Get("Content-Type"), data)
	if !allowedType(ct, r.Path) {
		return File{}, fail(r.URL, fmt.Sprintf("content type %q is not an image, font, stylesheet or script", ct))
	}
	f := File{URL: r.URL, Path: r.Path, Data: data, ContentType: ct, OriginalBytes: int64(len(data))}
	if small, ok := Downscale(data, ct); ok {
		f.Data, f.Resized = small, true
	}
	return f, nil
}

func describe(err error) string {
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return "the address points to a private or reserved network"
	case errors.Is(err, context.DeadlineExceeded):
		return "the server did not answer in time"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var ne *net.OpError
	if errors.As(err, &ne) && ne.Err != nil {
		err = ne.Err
	}
	return "download failed: " + err.Error()
}

func contentType(header string, data []byte) string {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil || mt == "" {
		mt, _, _ = mime.ParseMediaType(http.DetectContentType(data[:min(len(data), sniffBytes)]))
	}
	return strings.ToLower(mt)
}

var allowedTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"image/svg+xml": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
	"font/woff": true, "font/woff2": true, "font/ttf": true, "font/otf": true, "font/sfnt": true,
	"application/font-woff": true, "application/font-woff2": true, "application/x-font-woff": true,
	"application/x-font-ttf": true, "application/x-font-otf": true, "application/vnd.ms-fontobject": true,
	"text/css": true, "text/javascript": true, "application/javascript": true, "application/x-javascript": true,
}

// genericTypes are accepted when the target extension is a font or image:
// CDNs often serve fonts this way.
var genericTypes = map[string]bool{"application/octet-stream": true, "binary/octet-stream": true}

var allowedExts = map[string]string{
	".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image", ".webp": "image", ".avif": "image",
	".svg": "image", ".ico": "image",
	".woff": "font", ".woff2": "font", ".ttf": "font", ".otf": "font", ".eot": "font",
	".css": "style", ".js": "script", ".mjs": "script",
}

// AllowedPath reports whether a fetched file may be stored at p.
func AllowedPath(p string) bool {
	_, ok := allowedExts[strings.ToLower(path.Ext(p))]
	return ok
}

func allowedType(ct, p string) bool {
	if allowedTypes[ct] {
		return true
	}
	kind := allowedExts[strings.ToLower(path.Ext(p))]
	return genericTypes[ct] && (kind == "font" || kind == "image")
}
