package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
)

// LoginResult is a new UI session. Secret goes into the cookie; it is never stored.
type LoginResult struct {
	User    domain.User
	Secret  string
	Session domain.Session
}

func ipKey(ip string) string { return "ip:" + ip }

// Login verifies credentials and opens a UI session. Unknown email, wrong
// password, a blocked or not-yet-activated user all yield the same error.
func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (LoginResult, error) {
	if ok, retry := s.loginLimit.Check(ipKey(ip)); !ok {
		return LoginResult{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	u, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(email))
	hash := s.hasher.dummy
	if err == nil && u.PasswordHash != "" {
		hash = u.PasswordHash
	}
	if err != nil && domain.AsError(err).Kind != domain.KindNotFound {
		return LoginResult{}, err
	}
	match, verr := s.hasher.verify(ctx, password, hash)
	if verr != nil {
		return LoginResult{}, fmt.Errorf("verify password: %w", verr)
	}
	if err != nil || !match || u.PasswordHash == "" || u.Status != domain.UserActive {
		s.loginLimit.Record(ipKey(ip))
		s.audit(ctx, Actor{IP: ip}, "auth.login_failed", "", map[string]any{"email": email})
		return LoginResult{}, domain.ErrBadCredentials
	}
	return s.openSession(ctx, u, ip, userAgent)
}

func (s *Service) openSession(ctx context.Context, u domain.User, ip, userAgent string) (LoginResult, error) {
	secret := domain.NewSecret()
	csrf := make([]byte, 32)
	if _, err := rand.Read(csrf); err != nil {
		return LoginResult{}, fmt.Errorf("generate csrf secret: %w", err)
	}
	now := s.now()
	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}
	sess := domain.Session{
		ID: domain.HashSecret(secret), UserID: u.ID, CSRFSecret: csrf,
		CreatedAt: now, ExpiresAt: now.Add(SessionTTL), LastSeenAt: now, IP: ip, UserAgent: userAgent,
	}
	if err := s.repo.CreateSession(ctx, sess); err != nil {
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}
	s.audit(ctx, Actor{User: u, IP: ip}, "auth.login", userTarget(u.ID), nil)
	return LoginResult{User: u, Secret: secret, Session: sess}, nil
}

// AuthenticateSession resolves a session cookie value. The session slides:
// activity extends it to SessionTTL from now.
func (s *Service) AuthenticateSession(ctx context.Context, secret, ip string) (Actor, domain.Session, error) {
	if secret == "" {
		return Actor{}, domain.Session{}, domain.ErrUnauthorized
	}
	id := domain.HashSecret(secret)
	sess, err := s.repo.GetSession(ctx, id)
	if err != nil {
		return Actor{}, domain.Session{}, domain.ErrUnauthorized
	}
	now := s.now()
	if !now.Before(sess.ExpiresAt) {
		_ = s.repo.DeleteSession(ctx, id)
		return Actor{}, domain.Session{}, domain.ErrUnauthorized
	}
	u, err := s.repo.GetUser(ctx, sess.UserID)
	if err != nil || u.Status != domain.UserActive {
		_ = s.repo.DeleteSession(ctx, id)
		return Actor{}, domain.Session{}, domain.ErrUnauthorized
	}
	if now.Sub(sess.LastSeenAt) > touchInterval {
		sess.LastSeenAt = now
		sess.ExpiresAt = now.Add(SessionTTL)
		if err := s.repo.TouchSession(ctx, id, now, sess.ExpiresAt); err != nil {
			s.log.Warn("touch session failed", "err", err)
		}
	}
	return Actor{User: u, IP: ip}, sess, nil
}

// Logout ends a session.
func (s *Service) Logout(ctx context.Context, secret string) error {
	if secret == "" {
		return nil
	}
	if err := s.repo.DeleteSession(ctx, domain.HashSecret(secret)); err != nil && domain.AsError(err).Kind != domain.KindNotFound {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// AuthenticateToken resolves an API bearer token.
func (s *Service) AuthenticateToken(ctx context.Context, token, ip string) (Actor, error) {
	if ok, retry := s.tokenLimit.Check(ipKey(ip)); !ok {
		return Actor{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	fail := func() (Actor, error) {
		s.tokenLimit.Record(ipKey(ip))
		return Actor{}, domain.ErrUnauthorized
	}
	if !strings.HasPrefix(token, brand.TokenPrefix) || len(token) > 128 {
		return fail()
	}
	t, err := s.repo.GetTokenByHash(ctx, domain.HashSecret(token))
	now := s.now()
	if err != nil || !t.Active(now) {
		return fail()
	}
	u, err := s.repo.GetUser(ctx, t.UserID)
	if err != nil || u.Status != domain.UserActive {
		return fail()
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > touchInterval {
		if err := s.repo.TouchToken(ctx, t.ID, now); err != nil {
			s.log.Warn("touch token failed", "err", err)
		}
	}
	return Actor{User: u, IP: ip}, nil
}

// NewToken is a freshly issued API token; Value is shown only once.
type NewToken struct {
	Token domain.Token
	Value string
}

// CreateToken issues an API token for the actor. ttl 0 means no expiry.
func (s *Service) CreateToken(ctx context.Context, a Actor, name string, ttl time.Duration) (NewToken, error) {
	name, err := domain.ValidateTokenName(name)
	if err != nil {
		return NewToken{}, err
	}
	if ttl < 0 {
		return NewToken{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "expiry must be in the future")
	}
	value := domain.NewToken()
	now := s.now()
	t := domain.Token{
		UserID: a.User.ID, Name: name, Prefix: domain.TokenDisplayPrefix(value),
		Hash: domain.HashSecret(value), CreatedAt: now,
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		t.ExpiresAt = &exp
	}
	id, err := s.repo.CreateToken(ctx, t)
	if err != nil {
		return NewToken{}, fmt.Errorf("create token: %w", err)
	}
	t.ID = id
	s.audit(ctx, a, "token.create", fmt.Sprintf("token:%d", id), map[string]any{"name": name, "prefix": t.Prefix})
	return NewToken{Token: t, Value: value}, nil
}

// ListTokens returns the actor's tokens.
func (s *Service) ListTokens(ctx context.Context, a Actor) ([]domain.Token, error) {
	return s.repo.ListTokens(ctx, a.User.ID)
}

// RevokeToken revokes one of the actor's tokens; others' tokens are not found.
func (s *Service) RevokeToken(ctx context.Context, a Actor, tokenID int64) error {
	if err := s.repo.RevokeToken(ctx, a.User.ID, tokenID, s.now()); err != nil {
		return err
	}
	s.audit(ctx, a, "token.revoke", fmt.Sprintf("token:%d", tokenID), nil)
	return nil
}
