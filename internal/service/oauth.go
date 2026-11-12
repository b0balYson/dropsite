package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// OAuth policy (spec oauth).
const (
	OAuthScope         = "sites"
	AccessTokenTTL     = time.Hour
	RefreshTokenTTL    = 30 * 24 * time.Hour
	AuthCodeTTL        = time.Minute
	MaxOAuthClients    = 1000
	RegistrationsPerIP = 10
	RegistrationWindow = time.Hour
	// NeverUsedClientTTL removes a registration that never led to a
	// connection; real clients authorize right after registering.
	NeverUsedClientTTL = 24 * time.Hour
	// UnusedClientTTL removes a registration whose connections were all revoked.
	UnusedClientTTL = 30 * 24 * time.Hour
	// ClientEvictionGrace protects a fresh registration from eviction at the
	// cap while its user is still on the consent page.
	ClientEvictionGrace = 15 * time.Minute
	MaxRedirectURIs     = 10
	// ClientMetadataFetchesPerIP bounds outbound fetches of client metadata
	// documents that one network can trigger (cache hits are free).
	ClientMetadataFetchesPerIP = 10
	ClientMetadataFetchWindow  = 10 * time.Minute
	maxClientNameRunes         = 100
	maxRedirectURILength       = 2000
)

// OAuthError is an RFC 6749 error. Adapters write Code and Description as is.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthErr(code, desc string) error { return &OAuthError{Code: code, Description: desc} }

func invalidGrant() error {
	return oauthErr("invalid_grant", "the grant is invalid, expired, revoked or was issued to another client")
}

// MCPResource is the canonical URI of the MCP resource (token audience).
func (s *Service) MCPResource() string { return s.APIURL() + "/mcp" }

// Issuer is the OAuth authorization server identifier.
func (s *Service) Issuer() string { return s.APIURL() }

// CanonicalResource maps a requested resource indicator to the MCP resource.
// Empty means the MCP resource; the API origin is accepted as a synonym
// because some clients send the origin.
func (s *Service) CanonicalResource(r string) (string, bool) {
	r = strings.TrimSuffix(r, "/")
	switch r {
	case "", s.MCPResource(), s.Issuer():
		return s.MCPResource(), true
	}
	return "", false
}

// ValidRedirectURI reports whether u may be registered as a redirect URI:
// https, or http on a loopback address; never a fragment or credentials. The
// host must be a DNS name or an IP literal, so the origin is safe to place in
// a Content-Security-Policy.
func ValidRedirectURI(u string) bool {
	if u == "" || len(u) > maxRedirectURILength {
		return false
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" || p.Fragment != "" || p.User != nil || p.Opaque != "" || !validRedirectHost(p.Hostname()) {
		return false
	}
	switch p.Scheme {
	case "https":
		return true
	case "http":
		return isLoopbackHost(p.Hostname())
	}
	return false
}

// validRedirectHost accepts a DNS name (letters, digits, hyphens and dots)
// or an IP literal without a zone.
func validRedirectHost(h string) bool {
	if ip, err := netip.ParseAddr(h); err == nil {
		return ip.Zone() == ""
	}
	return domain.IsValidHostname(strings.ToLower(h))
}

// RedirectOrigin returns the origin (scheme://host[:port]) of a valid
// redirect URI, or "" when the URI is not valid.
func RedirectOrigin(u string) string {
	if !ValidRedirectURI(u) {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Scheme + "://" + p.Host
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// RedirectHost is the host shown to users for a redirect URI.
func RedirectHost(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Host
}

// CleanClientName makes a client-supplied name safe to display: control and
// format characters (including bidirectional overrides) are dropped, white
// space is collapsed and the length is capped.
func CleanClientName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.In(r, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs) {
			if unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > maxClientNameRunes {
		name = string(r[:maxClientNameRunes])
	}
	return name
}

// RegisterOAuthClient registers a public client (RFC 7591).
func (s *Service) RegisterOAuthClient(ctx context.Context, name string, redirectURIs []string, ip string) (domain.OAuthClient, error) {
	if len(redirectURIs) == 0 || len(redirectURIs) > MaxRedirectURIs {
		return domain.OAuthClient{}, oauthErr("invalid_redirect_uri",
			fmt.Sprintf("provide between 1 and %d redirect_uris", MaxRedirectURIs))
	}
	for _, u := range redirectURIs {
		if !ValidRedirectURI(u) {
			return domain.OAuthClient{}, oauthErr("invalid_redirect_uri",
				"redirect_uris must be https URLs or http URLs on a loopback address, without fragments")
		}
	}
	if ok, retry := s.regLimit.Allow(ipKey(ip)); !ok {
		return domain.OAuthClient{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	name = CleanClientName(name)
	if name == "" {
		name = RedirectHost(redirectURIs[0])
	}
	now := s.now()
	c := domain.OAuthClient{
		ID: domain.NewOAuthClientID(), Name: name, RedirectURIs: redirectURIs, CreatedAt: now, CreatedIP: ip,
	}
	// At the cap the oldest registration without a connection makes room, so
	// a flood of registrations cannot lock real clients out.
	evicted, err := s.repo.CreateOAuthClientCapped(ctx, c, MaxOAuthClients, now.Add(-ClientEvictionGrace))
	if errors.Is(err, store.ErrOAuthClientLimit) {
		return domain.OAuthClient{}, oauthErr("temporarily_unavailable", "the client registration limit is reached")
	}
	if err != nil {
		return domain.OAuthClient{}, err
	}
	meta := map[string]any{"name": c.Name}
	if evicted != "" {
		meta["evicted"] = evicted
	}
	s.audit(ctx, Actor{IP: ip}, "oauth.client_register", "oauth_client:"+c.ID, meta)
	return c, nil
}

// AllowClientMetadataFetch admits one outbound fetch of a client metadata
// document on behalf of the client at ip.
func (s *Service) AllowClientMetadataFetch(ip string) error {
	if ok, retry := s.cimdLimit.Allow(ipKey(ip)); !ok {
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	return nil
}

// GetOAuthClient returns a registered client.
func (s *Service) GetOAuthClient(ctx context.Context, id string) (domain.OAuthClient, error) {
	return s.repo.GetOAuthClient(ctx, id)
}

// AuthorizeRequest is a validated authorization request.
type AuthorizeRequest struct {
	ClientID    string
	ClientName  string
	RedirectURI string
	Challenge   string
	Resource    string // canonical
	Scope       string
}

// AuthorizeOAuth approves a request with a connect code and returns a new
// authorization code. The request must already be validated by the adapter
// (client, redirect URI, PKCE); the connect code identifies the user.
func (s *Service) AuthorizeOAuth(ctx context.Context, req AuthorizeRequest, connectCode, ip string) (string, error) {
	if req.Challenge == "" || req.ClientID == "" || req.RedirectURI == "" {
		return "", oauthErr("invalid_request", "incomplete authorization request")
	}
	if _, ok := s.CanonicalResource(req.Resource); !ok {
		return "", oauthErr("invalid_target", "unknown resource")
	}
	u, err := s.redeemConnectCode(ctx, connectCode, ip, "oauth")
	if err != nil {
		return "", err
	}
	code := domain.NewSecret()
	now := s.now()
	if _, err := s.repo.CreateOAuthCode(ctx, domain.OAuthCode{
		Hash: domain.HashSecret(code), UserID: u.ID, ClientID: req.ClientID, ClientName: CleanClientName(req.ClientName),
		RedirectURI: req.RedirectURI, Challenge: req.Challenge, Resource: s.MCPResource(), Scope: OAuthScope,
		CreatedAt: now, ExpiresAt: now.Add(AuthCodeTTL),
	}); err != nil {
		return "", fmt.Errorf("create authorization code: %w", err)
	}
	s.audit(ctx, Actor{User: u, IP: ip}, "oauth.authorize", userTarget(u.ID), map[string]any{
		"client": CleanClientName(req.ClientName), "redirect_host": RedirectHost(req.RedirectURI),
	})
	return code, nil
}

// OAuthTokens is a token endpoint response.
type OAuthTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
}

func (s *Service) newTokenPair(now time.Time) (OAuthTokens, []domain.OAuthToken) {
	access := domain.NewOAuthToken(domain.OAuthAccessPrefix)
	refresh := domain.NewOAuthToken(domain.OAuthRefreshPrefix)
	return OAuthTokens{
		AccessToken: access, RefreshToken: refresh, ExpiresIn: int(AccessTokenTTL.Seconds()), Scope: OAuthScope,
	}, []domain.OAuthToken{
		{Hash: domain.HashSecret(access), Kind: domain.OAuthAccess, CreatedAt: now, ExpiresAt: now.Add(AccessTokenTTL)},
		{Hash: domain.HashSecret(refresh), Kind: domain.OAuthRefresh, CreatedAt: now, ExpiresAt: now.Add(RefreshTokenTTL)},
	}
}

// PKCEChallenge returns the S256 challenge of a verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const verifierAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

// validVerifier checks RFC 7636 length and alphabet.
func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !strings.ContainsRune(verifierAlphabet, r) {
			return false
		}
	}
	return true
}

// ExchangeOAuthCode redeems an authorization code (grant_type=authorization_code).
// Redeeming the code and issuing the grant happen in one transaction, so a
// replay racing the first exchange always finds the grant and revokes it.
func (s *Service) ExchangeOAuthCode(ctx context.Context, code, verifier, clientID, redirectURI, resource string) (OAuthTokens, error) {
	now := s.now()
	var out OAuthTokens
	accept := func(c domain.OAuthCode) (domain.OAuthGrant, []domain.OAuthToken, error) {
		if c.ClientID != clientID || c.RedirectURI != redirectURI {
			return domain.OAuthGrant{}, nil, invalidGrant()
		}
		if !validVerifier(verifier) || !domain.EqualHash([]byte(PKCEChallenge(verifier)), []byte(c.Challenge)) {
			return domain.OAuthGrant{}, nil, oauthErr("invalid_grant", "code_verifier does not match the code_challenge")
		}
		if res, ok := s.CanonicalResource(resource); !ok || res != c.Resource {
			return domain.OAuthGrant{}, nil, oauthErr("invalid_target", "the resource does not match the authorization request")
		}
		u, err := s.repo.GetUser(ctx, c.UserID)
		if err != nil || u.Status != domain.UserActive {
			return domain.OAuthGrant{}, nil, invalidGrant()
		}
		var rows []domain.OAuthToken
		out, rows = s.newTokenPair(now)
		return domain.OAuthGrant{
			UserID: u.ID, ClientID: c.ClientID, ClientName: c.ClientName, RedirectHost: RedirectHost(c.RedirectURI),
			Scope: c.Scope, Resource: c.Resource, CreatedAt: now,
		}, rows, nil
	}
	_, err := s.repo.ExchangeOAuthCode(ctx, domain.HashSecret(code), now, accept)
	if errors.Is(err, store.ErrCredentialReused) {
		s.audit(ctx, Actor{}, "oauth.code_reuse", "", map[string]any{"client": clientID})
		return OAuthTokens{}, invalidGrant()
	}
	var oe *OAuthError
	if errors.As(err, &oe) {
		return OAuthTokens{}, err
	}
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return OAuthTokens{}, invalidGrant()
		}
		return OAuthTokens{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	return out, nil
}

// RefreshOAuth rotates a refresh token (grant_type=refresh_token). A second
// use of the same refresh token revokes the whole grant.
func (s *Service) RefreshOAuth(ctx context.Context, refresh, clientID, resource string) (OAuthTokens, error) {
	if !strings.HasPrefix(refresh, domain.OAuthRefreshPrefix) || len(refresh) > 128 {
		return OAuthTokens{}, invalidGrant()
	}
	t, g, err := s.repo.GetOAuthToken(ctx, domain.HashSecret(refresh))
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return OAuthTokens{}, invalidGrant()
		}
		return OAuthTokens{}, err
	}
	if t.Kind != domain.OAuthRefresh || g.ClientID != clientID {
		return OAuthTokens{}, invalidGrant()
	}
	if res, ok := s.CanonicalResource(resource); !ok || res != g.Resource {
		return OAuthTokens{}, oauthErr("invalid_target", "the resource does not match the grant")
	}
	u, err := s.repo.GetUser(ctx, g.UserID)
	if err != nil || u.Status != domain.UserActive {
		return OAuthTokens{}, invalidGrant()
	}
	now := s.now()
	out, rows := s.newTokenPair(now)
	err = s.repo.RotateOAuthRefresh(ctx, t.ID, g.ID, now, rows)
	if errors.Is(err, store.ErrCredentialReused) {
		if g.RevokedAt == nil && t.RevokedAt == nil && now.Before(t.ExpiresAt) {
			s.audit(ctx, Actor{User: u}, "oauth.refresh_reuse", grantTarget(g.ID), map[string]any{"client": g.ClientName})
		}
		return OAuthTokens{}, invalidGrant()
	}
	if err != nil {
		return OAuthTokens{}, fmt.Errorf("rotate refresh token: %w", err)
	}
	return out, nil
}

func grantTarget(id int64) string { return fmt.Sprintf("oauth_grant:%d", id) }

// RevokeOAuthToken revokes the grant of an access or refresh token (RFC
// 7009). Unknown tokens are ignored.
func (s *Service) RevokeOAuthToken(ctx context.Context, token string) error {
	if !strings.HasPrefix(token, domain.OAuthAccessPrefix) && !strings.HasPrefix(token, domain.OAuthRefreshPrefix) {
		return nil
	}
	_, g, err := s.repo.GetOAuthToken(ctx, domain.HashSecret(token))
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return nil
		}
		return err
	}
	if g.RevokedAt != nil {
		return nil
	}
	if err := s.repo.RevokeOAuthGrant(ctx, g.ID, s.now()); err != nil {
		return err
	}
	s.audit(ctx, Actor{User: domain.User{ID: g.UserID}}, "oauth.revoke", grantTarget(g.ID),
		map[string]any{"client": g.ClientName, "by": "client"})
	return nil
}

// AuthenticateMCP resolves a bearer token presented to the MCP endpoint: an
// API token, or an OAuth access token issued for the MCP resource. It returns
// the actor and the token expiry (zero when the token does not expire).
func (s *Service) AuthenticateMCP(ctx context.Context, token, ip string) (Actor, time.Time, error) {
	if strings.HasPrefix(token, brand.TokenPrefix) {
		a, err := s.AuthenticateToken(ctx, token, ip)
		a.ViaMCP = true
		return a, time.Time{}, err
	}
	res, ok, retry := s.tokenLimit.Reserve(ipKey(ip))
	if !ok {
		return Actor{}, time.Time{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	fail := func() (Actor, time.Time, error) { return Actor{}, time.Time{}, domain.ErrUnauthorized }
	if !strings.HasPrefix(token, domain.OAuthAccessPrefix) || len(token) > 128 {
		return fail()
	}
	t, g, err := s.repo.GetOAuthToken(ctx, domain.HashSecret(token))
	now := s.now()
	if err != nil || t.Kind != domain.OAuthAccess || t.RevokedAt != nil || g.RevokedAt != nil ||
		!now.Before(t.ExpiresAt) || g.Resource != s.MCPResource() {
		return fail()
	}
	u, err := s.repo.GetUser(ctx, g.UserID)
	if err != nil || u.Status != domain.UserActive {
		return fail()
	}
	if g.LastUsedAt == nil || now.Sub(*g.LastUsedAt) > touchInterval {
		if err := s.repo.TouchOAuthGrant(ctx, g.ID, now); err != nil {
			s.log.Warn("touch oauth grant failed", "err", err)
		}
	}
	res.Cancel()
	return Actor{User: u, IP: ip, ViaMCP: true, GrantID: g.ID}, t.ExpiresAt, nil
}

// ListOAuthGrants returns the actor's connected applications.
func (s *Service) ListOAuthGrants(ctx context.Context, a Actor) ([]domain.OAuthGrant, error) {
	return s.repo.ListOAuthGrants(ctx, a.User.ID)
}

// RevokeOAuthGrant disconnects one of the actor's applications.
func (s *Service) RevokeOAuthGrant(ctx context.Context, a Actor, grantID int64) error {
	g, err := s.repo.RevokeUserOAuthGrant(ctx, a.User.ID, grantID, s.now())
	if err != nil {
		return err
	}
	s.audit(ctx, a, "oauth.revoke", grantTarget(g.ID), map[string]any{"client": g.ClientName, "by": "user"})
	return nil
}

// housekeepConnect drops expired connect codes, OAuth codes and tokens and
// stale client registrations.
func (s *Service) housekeepConnect(ctx context.Context, now time.Time) error {
	if _, err := s.repo.DeleteExpiredConnectCodes(ctx, now.Add(-time.Hour)); err != nil {
		return fmt.Errorf("delete expired connect codes: %w", err)
	}
	if _, err := s.repo.DeleteExpiredOAuth(ctx, now); err != nil {
		return fmt.Errorf("delete expired oauth state: %w", err)
	}
	if _, err := s.repo.DeleteUnusedOAuthClients(ctx, now.Add(-NeverUsedClientTTL), now.Add(-UnusedClientTTL)); err != nil {
		return fmt.Errorf("delete unused oauth clients: %w", err)
	}
	return nil
}
