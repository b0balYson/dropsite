package service

import (
	"context"
	"fmt"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Altcha proof of work shared by site forms and the visitor login page of
// protected sites. Challenges are signed with their purpose, so a solution
// is spent only where it was issued; difficulty escalates per (purpose, IP,
// site) and issuance is rate limited per (purpose, IP).

// escalationKey identifies the source whose difficulty escalates.
func escalationKey(purpose, ip, siteID string) string { return purpose + "|" + ip + "|" + siteID }

// issueAltcha creates a challenge for host. Every challenge from the same
// IP and site within the escalation window is four times harder than the
// previous one, up to a ceiling; floor is the lowest level to issue.
func (s *Service) issueAltcha(ctx context.Context, purpose, siteID, host, ip string, floor int) (altcha.Challenge, error) {
	if ok, retry := s.challengeIP.Allow(purpose + ":" + ip); !ok {
		return altcha.Challenge{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return altcha.Challenge{}, err
	}
	level := max(s.escalator.Next(escalationKey(purpose, ip, siteID)), floor)
	ch, err := s.altcha.Challenge(host, purpose, forms.Difficulty(cfg.altchaMax, level), time.Now())
	if err != nil {
		return altcha.Challenge{}, fmt.Errorf("create altcha challenge: %w", err)
	}
	return ch, nil
}

// spendAltcha verifies a solution for host and purpose and marks it used.
// false means missing, malformed, forged, expired, foreign or replayed.
func (s *Service) spendAltcha(ctx context.Context, purpose, host, payload string) (bool, error) {
	v, err := s.altcha.Verify(payload, host, purpose)
	if err != nil {
		return false, nil //nolint:nilerr // an invalid solution is an answer, not a failure
	}
	return s.repo.UseAltchaSolution(ctx, v.Signature, v.ExpiresAt)
}

// penalizeAltcha raises the difficulty for the source at once (a rejected
// submission or a failed login).
func (s *Service) penalizeAltcha(purpose, ip, siteID string) {
	s.escalator.Penalize(escalationKey(purpose, ip, siteID))
}
