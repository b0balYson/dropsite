// Package siteauth lets visitors into password-protected sites. It is an
// adapter over internal/service with two handlers:
//
//   - AuthCheck answers Caddy's forward_auth on the internal listener for
//     every request to a site that carries the protected marker;
//   - Login serves /_dropsite/login on the site's own host (Caddy proxies
//     /_dropsite/* to the public listener) and issues the access cookie.
//
// Open sites never reach this package: Caddy calls AuthCheck only when the
// marker exists.
package siteauth

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/service"
)

// Paths on the site hosts.
const (
	Prefix    = "/_" + brand.Name + "/"
	LoginPath = Prefix + "login"
	cssPath   = Prefix + "login.css"
	maxNext   = 2048
)

// requestHost returns the host of the request without a port. Caddy passes
// the visitor's Host header through unchanged to both listeners.
func requestHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return h
}

// safeNext keeps only a local absolute path: it must start with a single
// "/", contain no backslash or control characters and not point back at the
// platform paths. Anything else becomes "/".
func safeNext(next string) string {
	if next == "" || len(next) > maxNext || next[0] != '/' {
		return "/"
	}
	if strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\") || strings.HasPrefix(next, Prefix) ||
		next == strings.TrimSuffix(Prefix, "/") {
		return "/"
	}
	for i := range len(next) {
		if c := next[i]; c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return next
}

// loginURL is where AuthCheck sends a visitor without access.
func loginURL(next string) string {
	return LoginPath + "?" + url.Values{"next": {safeNext(next)}}.Encode()
}

// AuthCheck returns the forward_auth handler for the internal listener:
// 200 lets Caddy serve the file, 302 sends the visitor to the login page,
// 404 refuses an unknown host and 503 refuses when the check itself fails.
func AuthCheck(svc *service.Service, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHost(r)
		w.Header().Set("Cache-Control", "no-store")
		cookie := ""
		if c, err := r.Cookie(service.VisitorCookieName); err == nil {
			cookie = c.Value
		}
		d, err := svc.CheckVisitorAccess(r.Context(), host, cookie)
		log.Debug("site auth check", "host", host, "decision", int(d), "err", err)
		switch {
		case err != nil:
			log.Error("site auth check failed", "host", host, "err", err)
			http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
		case d == service.AccessAllow:
			w.WriteHeader(http.StatusOK)
		case d == service.AccessLogin:
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			http.Redirect(w, r, loginURL(r.Header.Get("X-Forwarded-Uri")), http.StatusFound)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
}

var errCSRF = errors.New("csrf check failed")
