package ui

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/service"
)

// publicHSTS is sent by Caddy as well; control repeats it so the policy does
// not depend on one place.
const publicHSTS = "max-age=31536000"

// publicMode is the configuration of the public admin listener: Caddy
// serves https://<host> to the internet and proxies it here.
type publicMode struct {
	host    string // admin.<base domain>
	port    int    // the HTTPS port visitors use (443 in production)
	origin  string // https://<host>[:port], the only accepted Origin
	trusted []netip.Prefix
}

// NewPublic returns the handler of the public admin listener: the same
// interface as New, but
//   - every response is a bare 404 while public access is off in the
//     settings, or when the Host is not the public admin host;
//   - the client address is the one X-Forwarded-For hop Caddy writes, read
//     only from a trusted proxy (TRUSTED_PROXIES, the compose network);
//   - cookies are Secure and carry the __Host- prefix;
//   - unsafe requests must come from the public origin exactly.
func NewPublic(svc *service.Service, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s, err := newServer(svc, log)
	if err != nil {
		panic(fmt.Sprintf("ui: %v", err))
	}
	cfg := svc.Config()
	s.public = &publicMode{
		host: svc.PublicAdminHost(), port: cfg.SitesHTTPSPort,
		origin: svc.PublicAdminURL(), trusted: cfg.TrustedProxies,
	}
	return s.publicGate(securityHeaders(s.routes()))
}

// publicGate answers every request with a bare 404, before any cookie is
// set or any route runs, unless public access is on and the request is for
// the public admin host.
func (s *server) publicGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.public.hostAllowed(r.Host) || !s.svc.PublicAdminEnabled(r.Context()) {
			h := w.Header()
			h.Set("Content-Type", "text/plain; charset=utf-8")
			h.Set("Cache-Control", "no-store")
			h.Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "404 page not found\n")
			return
		}
		w.Header().Set("Strict-Transport-Security", publicHSTS)
		next.ServeHTTP(w, r)
	})
}

// hostAllowed accepts the public admin host, optionally with the HTTPS
// port visitors use, case-insensitively.
func (p *publicMode) hostAllowed(hostport string) bool {
	h := strings.ToLower(strings.TrimSuffix(hostport, "."))
	if h == p.host {
		return true
	}
	name, port, ok := strings.Cut(h, ":")
	if !ok || name != p.host {
		return false
	}
	return port == strconv.Itoa(p.port) || (port == "443" && p.port == 443)
}

// clientIP is the address the requests of this listener are counted and
// audited under. The loopback listener has no proxy in front: the peer.
// The public one is reached only through Caddy, which replaces
// X-Forwarded-For with exactly one hop; that hop is believed only when the
// peer is a trusted proxy, and only the rightmost value is read.
func (s *server) clientIP(r *http.Request) string {
	if s.public == nil {
		return clientIP(r)
	}
	return forwardedIP(r, s.public.trusted)
}

func forwardedIP(r *http.Request, trusted []netip.Prefix) string {
	peer := clientIP(r)
	addr, err := netip.ParseAddr(peer)
	if err != nil || !inPrefixes(addr.Unmap(), trusted) {
		return peer
	}
	var last string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				last = p
			}
		}
	}
	hop, err := netip.ParseAddr(last)
	if err != nil {
		return peer
	}
	return hop.Unmap().String()
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// sameOrigin rejects cross-site requests. On the public listener the
// Origin, when the browser sends one, must be the public admin origin
// exactly: a published site on a sibling subdomain is same-site, and
// Sec-Fetch-Site "same-site" is refused as well.
func (s *server) sameOrigin(r *http.Request) bool {
	if !sameOrigin(r) {
		return false
	}
	if s.public == nil {
		return true
	}
	origin := r.Header.Get("Origin")
	return origin == "" || strings.EqualFold(origin, s.public.origin)
}
