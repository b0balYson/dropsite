package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ConnectURLTTL bounds a connect URL: an MCP address that carries a one-time
// connect code in its path (https://api.<domain>/mcp/c/<code>). It lives
// longer than a typed code because it is pasted into a chat client's
// settings, which takes a few steps.
const ConnectURLTTL = time.Hour

// IssuedConnectURL is a fresh connect URL; it is shown once.
type IssuedConnectURL struct {
	URL       string
	ExpiresAt time.Time
}

// ConnectURL is the MCP address that carries code.
func (s *Service) ConnectURL(code string) string {
	return s.MCPResource() + domain.ConnectLinkSegment + domain.NormalizeConnectCode(code)
}

// CreateConnectURL issues a connect URL for the actor. The code inside is an
// ordinary connect code (same storage, same single use) with a longer life.
func (s *Service) CreateConnectURL(ctx context.Context, a Actor) (IssuedConnectURL, error) {
	c, err := s.issueConnectCode(ctx, a, domain.NewConnectCode(), ConnectURLTTL, "connect.url_create", nil)
	if err != nil {
		return IssuedConnectURL{}, err
	}
	return IssuedConnectURL{URL: s.ConnectURL(c.Code), ExpiresAt: c.ExpiresAt}, nil
}

// CreateConnectURLForEmail issues a connect URL for the active user with this
// e-mail. Only the server command (control connect-url) calls it: whoever
// runs it already controls the server.
func (s *Service) CreateConnectURLForEmail(ctx context.Context, email string) (IssuedConnectURL, domain.User, error) {
	u, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return IssuedConnectURL{}, domain.User{}, err
	}
	if u.Status != domain.UserActive {
		return IssuedConnectURL{}, u, domain.E(domain.KindInvalid, domain.CodeConnectInvalid,
			"the account is not active: activate it in the web interface first")
	}
	c, err := s.issueConnectCode(ctx, Actor{User: u}, domain.NewConnectCode(), ConnectURLTTL, "connect.url_create",
		map[string]any{"source": string(domain.SourceCLI)})
	if err != nil {
		return IssuedConnectURL{}, u, err
	}
	return IssuedConnectURL{URL: s.ConnectURL(c.Code), ExpiresAt: c.ExpiresAt}, u, nil
}

// LinkCode returns the normalized connect code of a resource indicator that
// is a connect URL, or "" when resource is not one. Every well-formed connect
// URL is an alias of the MCP resource, valid code or not: the address is not
// a credential, and treating them alike reveals nothing about codes.
func (s *Service) LinkCode(resource string) string {
	rest, ok := strings.CutPrefix(strings.TrimSuffix(resource, "/"), s.MCPResource()+domain.ConnectLinkSegment)
	if !ok {
		return ""
	}
	return domain.ConnectCodeFromLinkPath(rest)
}

// CheckConnectLink reports whether the code of a connect URL can still
// authorize, without consuming it, so the consent page can skip the code
// field. A failure counts against the same per-network limit as a mistyped
// code.
func (s *Service) CheckConnectLink(ctx context.Context, code, ip string) error {
	res, ok, retry := s.codeLimit.Reserve(ipKey(ip))
	if !ok {
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	norm := domain.NormalizeConnectCode(code)
	if norm == "" {
		return s.connectFailed(ctx, ip, "oauth_link")
	}
	userID, err := s.repo.PeekConnectCode(ctx, domain.HashSecret(norm), s.now())
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return s.connectFailed(ctx, ip, "oauth_link")
		}
		res.Cancel()
		return fmt.Errorf("check connect link: %w", err)
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil || u.Status != domain.UserActive {
		return s.connectFailed(ctx, ip, "oauth_link")
	}
	res.Cancel()
	return nil
}

// AuthorizeOAuthLink approves a request whose resource carried a connect
// URL: the code of the URL identifies the user and is consumed here.
func (s *Service) AuthorizeOAuthLink(ctx context.Context, req AuthorizeRequest, code, ip string) (string, error) {
	return s.authorizeWithCode(ctx, req, code, ip, "oauth_link")
}

// AuthorizeOAuthSession approves a request for the user signed in to the web
// interface (the public admin host). Only a browser session may do this: an
// API token or an assistant never grants itself new access.
func (s *Service) AuthorizeOAuthSession(ctx context.Context, a Actor, req AuthorizeRequest) (string, error) {
	if a.User.ID == 0 || a.ViaMCP || a.TokenID != 0 || a.GrantID != 0 {
		return "", domain.ErrUnauthorized
	}
	if err := checkAuthorizeRequest(s, req); err != nil {
		return "", err
	}
	u, err := s.repo.GetUser(ctx, a.User.ID)
	if err != nil || u.Status != domain.UserActive {
		return "", domain.ErrUnauthorized
	}
	return s.issueAuthCode(ctx, u, req, a.IP, "session")
}
