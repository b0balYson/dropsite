package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Connect code policy (spec accounts-auth).
const (
	ConnectCodeTTL      = 10 * time.Minute
	FailedCodesPerIP    = 10
	FailedCodesWindow   = 15 * time.Minute
	connectTokenNameFmt = "Connected %s"
)

// errConnectInvalid is the single answer for unknown, used and expired codes
// and for codes of users who can no longer sign in.
var errConnectInvalid = domain.E(domain.KindInvalid, domain.CodeConnectInvalid,
	"the connect code is invalid, already used or expired; issue a new one on the Connect AI page")

// IssuedConnectCode is a freshly issued code; Code is shown once.
type IssuedConnectCode struct {
	Code      string
	ExpiresAt time.Time
}

// CreateConnectCode issues a one-time connect code for the actor. Earlier
// codes stay valid until they expire.
func (s *Service) CreateConnectCode(ctx context.Context, a Actor) (IssuedConnectCode, error) {
	return s.issueConnectCode(ctx, a, domain.NewConnectCode(), ConnectCodeTTL, "connect.code_create", nil)
}

// issueConnectCode stores the hash of code for the actor's user and audits
// the issue under action.
func (s *Service) issueConnectCode(ctx context.Context, a Actor, code string, ttl time.Duration,
	action string, meta map[string]any) (IssuedConnectCode, error) {
	if a.User.ID == 0 || a.ViaMCP {
		return IssuedConnectCode{}, domain.ErrUnauthorized
	}
	now := s.now()
	c := domain.ConnectCode{
		UserID: a.User.ID, Hash: domain.HashSecret(domain.NormalizeConnectCode(code)),
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	id, err := s.repo.CreateConnectCode(ctx, c)
	if err != nil {
		return IssuedConnectCode{}, fmt.Errorf("create connect code: %w", err)
	}
	s.audit(ctx, a, action, fmt.Sprintf("connect_code:%d", id), meta)
	return IssuedConnectCode{Code: code, ExpiresAt: c.ExpiresAt}, nil
}

// redeemConnectCode consumes a code and returns its active owner. Failures
// count against the per-IP limit shared by every place that accepts codes;
// the attempt is reserved before the lookup so parallel requests cannot
// exceed it.
func (s *Service) redeemConnectCode(ctx context.Context, code, ip, purpose string) (domain.User, error) {
	res, ok, retry := s.codeLimit.Reserve(ipKey(ip))
	if !ok {
		return domain.User{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	fail := func() (domain.User, error) { return domain.User{}, s.connectFailed(ctx, ip, purpose) }
	norm := domain.NormalizeConnectCode(code)
	if norm == "" {
		return fail()
	}
	userID, err := s.repo.RedeemConnectCode(ctx, domain.HashSecret(norm), s.now())
	if err != nil {
		if domain.AsError(err).Kind == domain.KindNotFound {
			return fail()
		}
		res.Cancel()
		return domain.User{}, fmt.Errorf("redeem connect code: %w", err)
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil || u.Status != domain.UserActive {
		return fail()
	}
	res.Cancel()
	s.audit(ctx, Actor{User: u, IP: ip}, "connect.code_use", userTarget(u.ID), map[string]any{"purpose": purpose})
	return u, nil
}

// connectFailed audits a failed code (one row per network and window: the
// rate limit already bounds guessing, and the first row is enough to see it
// happen) and returns the single answer for every failure.
func (s *Service) connectFailed(ctx context.Context, ip, purpose string) error {
	if ok, _ := s.codeAudit.Allow(ipKey(ip)); ok {
		s.audit(ctx, Actor{IP: ip}, "connect.code_failed", "", map[string]any{
			"purpose": purpose, "repeats_hidden_minutes": int(FailedCodesWindow.Minutes()),
		})
	}
	return errConnectInvalid
}

// ConnectResult is what exchanging a connect code yields for the CLI.
type ConnectResult struct {
	User  domain.User
	Token NewToken
}

// ExchangeConnectCode trades a connect code for a new API token of its owner.
func (s *Service) ExchangeConnectCode(ctx context.Context, code, name, ip string) (ConnectResult, error) {
	u, err := s.redeemConnectCode(ctx, code, ip, "token")
	if err != nil {
		return ConnectResult{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf(connectTokenNameFmt, s.now().Format("2006-01-02 15:04"))
	}
	if _, err := domain.ValidateTokenName(name); err != nil {
		name = fmt.Sprintf(connectTokenNameFmt, s.now().Format("2006-01-02 15:04"))
	}
	tok, err := s.CreateToken(ctx, Actor{User: u, IP: ip}, name, 0)
	if err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{User: u, Token: tok}, nil
}
