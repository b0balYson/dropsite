// Package oauth serves the OAuth 2.1 authorization server protocol for MCP
// clients on the API host: metadata (RFC 9728, RFC 8414), client
// registration (RFC 7591) and client metadata documents, the token and
// revocation endpoints, and validation of authorization requests for the
// consent page (rendered by internal/ui). Every grant decision is made by
// internal/service.
package oauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Paths on the API host.
const (
	AuthorizePath = "/oauth/authorize"
	TokenPath     = "/oauth/token" //nolint:gosec // G101: a URL path, not a credential
	RegisterPath  = "/oauth/register"
	RevokePath    = "/oauth/revoke"
	ResourceMeta  = "/.well-known/oauth-protected-resource"
	ServerMeta    = "/.well-known/oauth-authorization-server"
	// sealTTL bounds how long a consent form stays valid.
	sealTTL = 15 * time.Minute
)

// Client is a registered or metadata-document client.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	// Metadata is true for clients identified by a metadata document URL.
	Metadata bool
}

// Options configures the server.
type Options struct {
	// ClientIP returns the client address resolved by the API middleware.
	ClientIP func(*http.Request) string
	Logger   *slog.Logger
	// Resolver resolves client metadata hosts (tests use a fake).
	Resolver Resolver
	// TLSConfig is used to fetch client metadata documents (tests only).
	TLSConfig *tls.Config
	// AllowAddr overrides the public-address check (tests only).
	AllowAddr func(netip.Addr) bool
	// MetadataDialPort connects to this port instead of 443 when fetching
	// client metadata documents (tests only).
	MetadataDialPort string
}

// Server is the OAuth protocol adapter.
type Server struct {
	svc   *service.Service
	o     Options
	cimd  *fetcher
	key   []byte
	nowFn func() time.Time
}

// New returns the OAuth server.
func New(svc *service.Service, o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.ClientIP == nil {
		o.ClientIP = remoteHost
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	cimd := newFetcher(o.Resolver, o.TLSConfig, o.AllowAddr, o.MetadataDialPort)
	cimd.admit = svc.AllowClientMetadataFetch
	return &Server{svc: svc, o: o, cimd: cimd, key: key, nowFn: time.Now}
}

// remoteHost is the connection address without the port.
func remoteHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Issuer is the authorization server identifier.
func (s *Server) Issuer() string { return s.svc.Issuer() }

// Route is one handler of the server.
type Route struct {
	Pattern string
	Handler http.Handler
}

// Routes lists the protocol endpoints (the consent page is served by ui).
func (s *Server) Routes() []Route {
	prm := s.resourceMetadata()
	return []Route{
		{ResourceMeta, prm},
		{ResourceMeta + "/mcp", prm},
		{ServerMeta, cors(http.HandlerFunc(s.serverMetadata))},
		{RegisterPath, cors(http.HandlerFunc(s.register))},
		{TokenPath, cors(http.HandlerFunc(s.token))},
		{RevokePath, cors(http.HandlerFunc(s.revoke))},
	}
}

// ResolveClient returns a registered client or fetches a metadata document.
// ip is the address of the browser that asked; outbound fetches are rate
// limited per address.
func (s *Server) ResolveClient(ctx context.Context, clientID, ip string) (Client, error) {
	if IsMetadataURL(clientID) {
		return s.cimd.get(ctx, clientID, ip)
	}
	if clientID == "" || len(clientID) > 64 {
		return Client{}, domain.ErrNotFound
	}
	c, err := s.svc.GetOAuthClient(ctx, clientID)
	if err != nil {
		return Client{}, err
	}
	return Client{ID: c.ID, Name: c.Name, RedirectURIs: c.RedirectURIs}, nil
}

// PageError is an authorization request error that must be shown to the
// user and must not redirect (unknown client or redirect URI).
type PageError struct{ Reason string }

func (e *PageError) Error() string { return e.Reason }

// RedirectError is an authorization request error reported to the client
// by redirecting to its redirect URI.
type RedirectError struct {
	RedirectURI string
	State       string
	Code        string
	Description string
}

func (e *RedirectError) Error() string { return e.Code + ": " + e.Description }

// Pending is a validated authorization request awaiting the user's consent.
type Pending struct {
	Request service.AuthorizeRequest
	State   string
	Client  Client
}

// ValidateAuthorize checks an authorization request (query parameters) sent
// by the browser at ip. A rate-limited metadata fetch returns the domain
// rate-limit error as is.
func (s *Server) ValidateAuthorize(ctx context.Context, q url.Values, ip string) (Pending, error) {
	for _, k := range []string{"client_id", "redirect_uri", "response_type", "state", "code_challenge",
		"code_challenge_method", "resource", "scope"} {
		if len(q[k]) > 1 {
			return Pending{}, &PageError{Reason: "the parameter " + k + " is repeated"}
		}
	}
	c, err := s.ResolveClient(ctx, q.Get("client_id"), ip)
	if errors.Is(err, domain.ErrRateLimited) {
		return Pending{}, err
	}
	if err != nil {
		s.o.Logger.Info("oauth authorize: unknown client", "client_id_len", len(q.Get("client_id")), "err", err)
		return Pending{}, &PageError{Reason: "the application is not registered or its metadata could not be loaded"}
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if !slices.Contains(c.RedirectURIs, redirect) {
		return Pending{}, &PageError{Reason: "the redirect address does not match the application's registration"}
	}
	state := q.Get("state")
	fail := func(code, desc string) (Pending, error) {
		return Pending{}, &RedirectError{RedirectURI: redirect, State: state, Code: code, Description: desc}
	}
	if q.Get("response_type") != "code" {
		return fail("unsupported_response_type", "only response_type=code is supported")
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || len(challenge) != 43 {
		return fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
	}
	if _, ok := s.svc.CanonicalResource(q.Get("resource")); !ok {
		return fail("invalid_target", "the resource must be "+s.svc.MCPResource())
	}
	if len(state) > 1024 {
		return fail("invalid_request", "state is too long")
	}
	return Pending{
		Request: service.AuthorizeRequest{
			ClientID: c.ID, ClientName: c.Name, RedirectURI: redirect, Challenge: challenge,
			Resource: s.svc.MCPResource(), Scope: service.OAuthScope,
		},
		State: state, Client: c,
	}, nil
}

type sealed struct {
	P   Pending `json:"p"`
	Exp int64   `json:"e"`
}

// Seal signs a pending request for a hidden form field, so the POST cannot
// change what the user saw.
func (s *Server) Seal(p Pending) string {
	b, _ := json.Marshal(sealed{P: p, Exp: s.nowFn().Add(sealTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.sign(payload)
}

func (s *Server) sign(payload string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// ErrExpiredForm means the consent form is forged or too old.
var ErrExpiredForm = errors.New("the authorization form expired; start the connection again from the application")

// Open verifies a sealed request.
func (s *Server) Open(v string) (Pending, error) {
	payload, sig, ok := strings.Cut(v, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return Pending{}, ErrExpiredForm
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Pending{}, ErrExpiredForm
	}
	var sd sealed
	if err := json.Unmarshal(b, &sd); err != nil || s.nowFn().Unix() > sd.Exp {
		return Pending{}, ErrExpiredForm
	}
	return sd.P, nil
}

// SuccessURL is the redirect carrying a new authorization code.
func (s *Server) SuccessURL(p Pending, code string) string {
	return withQuery(p.Request.RedirectURI, url.Values{"code": {code}, "state": optional(p.State), "iss": {s.Issuer()}})
}

// ErrorURL is the redirect carrying an authorization error.
func (s *Server) ErrorURL(e *RedirectError) string {
	return withQuery(e.RedirectURI, url.Values{
		"error": {e.Code}, "error_description": {e.Description}, "state": optional(e.State), "iss": {s.Issuer()},
	})
}

func optional(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

// withQuery adds parameters to a registered redirect URI, keeping its own.
func withQuery(base string, add url.Values) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vs := range add {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// cors allows cross-origin calls: these endpoints use no cookies.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, MCP-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// writeError maps service errors to RFC 6749 error responses.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		status := http.StatusBadRequest
		if oe.Code == "invalid_client" {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, errorBody{Error: oe.Code, Description: oe.Description})
		return
	}
	de := domain.AsError(err)
	switch de.Kind {
	case domain.KindRateLimited:
		w.Header().Set("Retry-After", fmt.Sprint(max(int64(de.RetryAfter.Seconds()+0.999), 1)))
		writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "slow_down", Description: "too many requests"})
	case domain.KindInternal:
		s.o.Logger.Error("oauth internal error", "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "server_error"})
	default:
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_request", Description: de.Message})
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, errorBody{Error: "invalid_request", Description: "method not allowed"})
}
