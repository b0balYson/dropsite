package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

type ctxKey int

const (
	infoKey ctxKey = iota
	actorKey
)

// requestInfo is filled while a request is handled and logged at the end.
type requestInfo struct {
	id     string
	ip     string
	route  string
	userID int64
}

func infoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(infoKey).(*requestInfo)
	return info
}

// actorFrom returns the authenticated actor; only authenticated routes call it.
func actorFrom(ctx context.Context) service.Actor {
	a, _ := ctx.Value(actorKey).(service.Actor)
	return a
}

// statusWriter remembers the response status for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush passes streamed responses (MCP server-sent events) on at once, also
// to handlers that check for http.Flusher instead of using a ResponseController.
func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// requestReadTimeout bounds reading the whole request of every endpoint except
// archive uploads, which may legitimately stream tens of megabytes over a slow
// link and rely on the server-wide ReadTimeout instead. A variable for tests.
var requestReadTimeout = 30 * time.Second

// isUpload reports whether r is an archive upload (POST /sites/{ref}/deploys).
func isUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, Prefix+"/sites/")
	if !ok {
		return false
	}
	ref, ok := strings.CutSuffix(rest, "/deploys")
	return ok && ref != "" && !strings.Contains(ref, "/")
}

// limitRead sets a short read deadline for non-upload requests, so a client
// cannot hold a connection open by trickling a small body. Writers that do
// not support deadlines (tests) are left alone.
func limitRead(w http.ResponseWriter, r *http.Request) {
	if isUpload(r) {
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(requestReadTimeout))
}

// middleware applies, outermost first: read deadline, security headers,
// request context and access log, panic recovery.
func (a *api) middleware(next http.Handler) http.Handler {
	trusted := a.svc.Config().TrustedProxies
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		limitRead(w, r)
		setSecurityHeaders(w.Header())
		info := &requestInfo{id: newRequestID(), ip: clientIP(r, trusted)}
		w.Header().Set("X-Request-Id", info.id)
		sw := &statusWriter{ResponseWriter: w}
		r = r.WithContext(context.WithValue(r.Context(), infoKey, info))
		a.recoverPanic(sw, r, next)
		a.logRequest(r, info, sw.status, time.Since(start))
	})
}

func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
}

func (a *api) recoverPanic(w *statusWriter, r *http.Request, next http.Handler) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
			panic(v)
		}
		a.log.Error("api panic", "request_id", infoFrom(r.Context()).id, "path", r.URL.Path, "panic", v)
		if w.status == 0 {
			writeJSON(w, http.StatusInternalServerError, errorBody(internalError()))
		}
	}()
	next.ServeHTTP(w, r)
}

func (a *api) logRequest(r *http.Request, info *requestInfo, status int, d time.Duration) {
	if status == 0 {
		status = http.StatusOK
	}
	attrs := []any{
		"request_id", info.id, "method", r.Method, "route", info.route, "status", status,
		"duration_ms", float64(d.Microseconds()) / 1000, "ip", info.ip,
	}
	if info.userID != 0 {
		attrs = append(attrs, "user_id", info.userID)
	}
	a.log.Info("api request", attrs...)
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// authenticated requires a bearer token. Cookies are never consulted.
func (a *api) authenticated(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			return errUnauthorized
		}
		info := infoFrom(r.Context())
		actor, err := a.svc.AuthenticateToken(r.Context(), token, info.ip)
		if err != nil {
			return err
		}
		// A client acting for an AI assistant (the local MCP server of the
		// CLI) gives up administrator powers like a token on /mcp. The
		// header can only lower rights, so it needs no proof.
		if isAssistant(r) {
			actor.ViaMCP = true
		}
		info.userID = actor.User.ID
		return next(w, r.WithContext(context.WithValue(r.Context(), actorKey, actor)))
	}
}

func bearerToken(h string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// clientIP returns RemoteAddr, or, when the peer is a trusted proxy, the
// rightmost X-Forwarded-For address. Only that one hop is believed: Caddy
// replaces the header with the address of its own peer, so anything to the
// left was written by the client and may be forged, even when the client
// itself sits in a trusted network (VPN, LAN, another container). A CDN in
// front of Caddy is configured in Caddy (trusted_proxies), which then still
// forwards exactly one correct hop (docs/security.md).
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	remote, ok := parseRemote(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !isTrusted(remote, trusted) {
		return remote.String()
	}
	hops := forwardedHops(r.Header.Values("X-Forwarded-For"))
	if len(hops) == 0 {
		return remote.String()
	}
	hop, err := netip.ParseAddr(hops[len(hops)-1])
	if err != nil {
		return remote.String()
	}
	return hop.Unmap().String()
}

func parseRemote(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap(), true
	}
	return netip.Addr{}, false
}

func forwardedHops(values []string) []string {
	var hops []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				hops = append(hops, p)
			}
		}
	}
	return hops
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
