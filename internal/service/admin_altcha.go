package service

import (
	"context"
	"fmt"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Altcha on the sign-in and invitation forms of the admin interface, on
// both the loopback and the public listener. Challenges are bound to the
// request host and to forms.PurposeAdmin, so a solution from a site form or
// a protected site's login page is never accepted here.
//
// The difficulty is the forms base level (Settings → forms). Unlike site
// forms it does not rise with every issued challenge: everybody behind the
// SSH tunnel shares one address, and a colleague opening the sign-in page a
// few times must not slow the others down. It rises one step (×4) after a
// failed sign-in from the address and stays there for the escalation window;
// brute force is bounded by the per-IP and per-account login limits.
const (
	// adminAltchaSite is the escalation scope of the admin forms (no site).
	adminAltchaSite = "-admin"
	// adminAltchaMaxLevel caps the escalation of the admin forms.
	adminAltchaMaxLevel = 1
)

// ErrAdminAltcha rejects a sign-in without a valid unused Altcha solution.
var ErrAdminAltcha = domain.E(domain.KindInvalid, domain.CodeFormCaptcha,
	"the browser check failed or expired; wait for it to finish and try again")

func adminAltchaKey(ip string) string {
	return escalationKey(forms.PurposeAdmin, ipKey(ip), adminAltchaSite)
}

// AdminAltchaChallenge issues a challenge for the admin forms at host.
// Issuance is rate limited per address like the other challenges.
func (s *Service) AdminAltchaChallenge(ctx context.Context, host, ip string) (altcha.Challenge, error) {
	if ok, retry := s.challengeIP.Allow(forms.PurposeAdmin + ":" + ipKey(ip)); !ok {
		return altcha.Challenge{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return altcha.Challenge{}, err
	}
	level := min(s.escalator.Level(adminAltchaKey(ip)), adminAltchaMaxLevel)
	ch, err := s.altcha.Challenge(host, forms.PurposeAdmin, forms.Difficulty(cfg.altchaMax, level), time.Now())
	if err != nil {
		return altcha.Challenge{}, fmt.Errorf("create altcha challenge: %w", err)
	}
	return ch, nil
}

// CheckAdminAltcha spends a solution for host; a missing, forged, expired,
// foreign or replayed one is ErrAdminAltcha and raises the difficulty for
// the source.
func (s *Service) CheckAdminAltcha(ctx context.Context, host, payload, ip string) error {
	fresh, err := s.spendAltcha(ctx, forms.PurposeAdmin, host, payload)
	if err != nil {
		return err
	}
	if !fresh {
		s.escalator.Penalize(adminAltchaKey(ip))
		return ErrAdminAltcha
	}
	return nil
}

// LoginInput is a submitted admin sign-in form.
type LoginInput struct {
	Email, Password string
	Altcha          string // the widget payload (base64 JSON)
	Host            string // request host the challenge was bound to
	IP, UserAgent   string
}

// LoginWithAltcha is the sign-in of the web interface: an exhausted per-IP
// limit refuses at once (no proof of work is spent), then the Altcha
// solution is checked, and only then the password, by Login with its
// timing-safe check and limits. A wrong password also raises the Altcha
// difficulty for the source.
func (s *Service) LoginWithAltcha(ctx context.Context, in LoginInput) (LoginResult, error) {
	if ok, retry := s.loginLimit.Check(ipKey(in.IP)); !ok {
		return LoginResult{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if err := s.CheckAdminAltcha(ctx, in.Host, in.Altcha, in.IP); err != nil {
		return LoginResult{}, err
	}
	res, err := s.Login(ctx, in.Email, in.Password, in.IP, in.UserAgent)
	if err != nil && domain.AsError(err).Code == domain.CodeBadCredentials {
		s.escalator.Penalize(adminAltchaKey(in.IP))
	}
	return res, err
}
