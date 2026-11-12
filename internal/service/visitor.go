package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
)

// Visitor access to password-protected sites.
const (
	// VisitorCookieName is the host-only cookie that grants access to a site.
	VisitorCookieName = brand.Name + "_access"
	VisitorCookieTTL  = 7 * 24 * time.Hour
	// Failed visitor logins: per (IP, site) and per site from all IPs.
	VisitorFailuresPerIP   = 10
	VisitorFailuresPerSite = 100
	VisitorFailuresWindow  = 15 * time.Minute
	// VisitorCSRFTTL bounds how long a rendered login form stays valid.
	VisitorCSRFTTL = time.Hour

	visitorSecretName = "visitor_hmac"
	visitorSecretLen  = 32
	visitorCookieTag  = "access-v1"
	visitorCSRFTag    = "csrf-v1"
)

type visitorKeySet struct {
	cookie []byte
	csrf   []byte
}

// visitorKeys loads (creating on first use) the installation secret and
// derives one key per purpose from it.
func (s *Service) visitorKeys(ctx context.Context) (*visitorKeySet, error) {
	s.visitorMu.Lock()
	defer s.visitorMu.Unlock()
	if s.visitorKeySet != nil {
		return s.visitorKeySet, nil
	}
	fresh := make([]byte, visitorSecretLen)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("generate visitor secret: %w", err)
	}
	secret, err := s.repo.EnsureSecret(ctx, visitorSecretName, fresh, s.now())
	if err != nil {
		return nil, fmt.Errorf("load visitor secret: %w", err)
	}
	if len(secret) < visitorSecretLen {
		return nil, errors.New("visitor secret is too short")
	}
	s.visitorKeySet = &visitorKeySet{cookie: mac(secret, visitorCookieTag), csrf: mac(secret, visitorCSRFTag)}
	return s.visitorKeySet, nil
}

func mac(key []byte, parts ...string) []byte {
	h := hmac.New(sha256.New, key)
	for _, p := range parts {
		// Length-prefixed, so ("ab","c") and ("a","bc") never collide.
		_, _ = h.Write([]byte(strconv.Itoa(len(p)) + ":" + p + "|"))
	}
	return h.Sum(nil)
}

var b64 = base64.RawURLEncoding

func cookieFields(siteID string, exp, version int64) []string {
	return []string{siteID, strconv.FormatInt(exp, 10), strconv.FormatInt(version, 10)}
}

func signCookie(key []byte, siteID string, exp time.Time, version int64) string {
	f := cookieFields(siteID, exp.Unix(), version)
	return strings.Join(f, ".") + "." + b64.EncodeToString(mac(key, f...))
}

// verifyCookie accepts only an unexpired cookie signed for this site and
// password version.
func verifyCookie(key []byte, value, siteID string, version int64, now time.Time) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != siteID {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= exp {
		return false
	}
	if v, err := strconv.ParseInt(parts[2], 10, 64); err != nil || v != version {
		return false
	}
	sig, err := b64.DecodeString(parts[3])
	if err != nil {
		return false
	}
	return hmac.Equal(sig, mac(key, parts[:3]...))
}

// AccessDecision is the answer to a protected-site request.
type AccessDecision int

const (
	// AccessUnknown: the host is not an active site address.
	AccessUnknown AccessDecision = iota
	// AccessAllow: the site is open or the visitor holds a valid cookie.
	AccessAllow
	// AccessLogin: the visitor must log in first.
	AccessLogin
)

// siteByHost resolves an active site by one of its hostnames.
func (s *Service) siteByHost(ctx context.Context, host string) (domain.Site, error) {
	if !domain.IsValidHostname(host) {
		return domain.Site{}, domain.ErrNotFound
	}
	return s.repo.FindSiteByHostname(ctx, host)
}

// CheckVisitorAccess decides whether a request to host carrying the given
// access cookie value may be served. Errors must be treated as a refusal.
func (s *Service) CheckVisitorAccess(ctx context.Context, host, cookie string) (AccessDecision, error) {
	site, err := s.siteByHost(ctx, host)
	if errors.Is(err, domain.ErrNotFound) {
		return AccessUnknown, nil
	}
	if err != nil {
		return AccessUnknown, err
	}
	if !site.Protected {
		return AccessAllow, nil
	}
	if cookie == "" {
		return AccessLogin, nil
	}
	keys, err := s.visitorKeys(ctx)
	if err != nil {
		return AccessUnknown, err
	}
	acc, err := s.repo.GetSiteAccess(ctx, site.ID)
	if err != nil {
		return AccessUnknown, err
	}
	if acc.PasswordHash != "" && verifyCookie(keys.cookie, cookie, site.ID, acc.Version, s.now()) {
		return AccessAllow, nil
	}
	return AccessLogin, nil
}

// VisitorSite returns the title of the protected site at host, for the login
// page. Unknown hosts and open sites are "not found".
func (s *Service) VisitorSite(ctx context.Context, host string) (string, error) {
	site, err := s.siteByHost(ctx, host)
	if err != nil {
		return "", err
	}
	if !site.Protected {
		return "", domain.ErrNotFound
	}
	return site.Title, nil
}

// VisitorCookie is a granted access cookie.
type VisitorCookie struct {
	Value   string
	Expires time.Time
}

var errVisitorPassword = domain.E(domain.KindUnauthorized, domain.CodeBadCredentials, "wrong password")

func visitorIPKey(ip, siteID string) string { return "visitor:" + siteID + ":" + ip }

// beginVisitorLogin reserves one failure against both limits before the slow
// password check; the caller cancels the reservations on success.
func (s *Service) beginVisitorLogin(siteID, ip string) (func(), func(), error) {
	release, ok := s.loginBusy.tryAcquire("visitor:" + ip)
	if !ok {
		return nil, nil, domain.ErrRateLimited.WithRetryAfter(LoginBusyRetryAfter)
	}
	ipRes, ok, retry := s.visitorIPLimit.Reserve(visitorIPKey(ip, siteID))
	if !ok {
		release()
		return nil, nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	siteRes, ok, retry := s.visitorSiteLimit.Reserve(siteID)
	if !ok {
		ipRes.Cancel()
		release()
		return nil, nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	refund := func() {
		for _, r := range []ratelimit.Reservation{ipRes, siteRes} {
			r.Cancel()
		}
	}
	return release, refund, nil
}

// VisitorLogin checks the site password and returns an access cookie.
// Failures are rate limited per (IP, site) and per site.
func (s *Service) VisitorLogin(ctx context.Context, host, password, ip string) (VisitorCookie, error) {
	site, err := s.siteByHost(ctx, host)
	if err != nil {
		return VisitorCookie{}, err
	}
	if !site.Protected {
		return VisitorCookie{}, domain.ErrNotFound
	}
	release, refund, err := s.beginVisitorLogin(site.ID, ip)
	if err != nil {
		return VisitorCookie{}, err
	}
	defer release()
	keys, err := s.visitorKeys(ctx)
	if err != nil {
		refund()
		return VisitorCookie{}, err
	}
	acc, err := s.repo.GetSiteAccess(ctx, site.ID)
	if err != nil {
		refund()
		return VisitorCookie{}, err
	}
	if acc.PasswordHash == "" {
		refund()
		return VisitorCookie{}, domain.ErrNotFound
	}
	if len(password) > domain.MaxPasswordLength {
		return VisitorCookie{}, errVisitorPassword
	}
	ok, err := s.hasher.verify(ctx, password, acc.PasswordHash)
	if err != nil {
		refund()
		return VisitorCookie{}, fmt.Errorf("verify site password: %w", err)
	}
	if !ok {
		return VisitorCookie{}, errVisitorPassword
	}
	refund()
	exp := s.now().Add(VisitorCookieTTL)
	return VisitorCookie{Value: signCookie(keys.cookie, site.ID, exp, acc.Version), Expires: exp}, nil
}

// VisitorCSRFToken binds a login form to host and to the nonce the adapter
// stores in a cookie (double submit); it is valid for VisitorCSRFTTL.
func (s *Service) VisitorCSRFToken(ctx context.Context, host, nonce string) (string, error) {
	keys, err := s.visitorKeys(ctx)
	if err != nil {
		return "", err
	}
	at := strconv.FormatInt(s.now().Unix(), 10)
	return at + "." + b64.EncodeToString(mac(keys.csrf, host, nonce, at)), nil
}

// CheckVisitorCSRF verifies a token from VisitorCSRFToken.
func (s *Service) CheckVisitorCSRF(ctx context.Context, host, nonce, token string) bool {
	if nonce == "" {
		return false
	}
	keys, err := s.visitorKeys(ctx)
	if err != nil {
		return false
	}
	at, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	issued, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return false
	}
	age := s.now().Sub(time.Unix(issued, 0))
	if age < -time.Minute || age > VisitorCSRFTTL {
		return false
	}
	raw, err := b64.DecodeString(sig)
	return err == nil && hmac.Equal(raw, mac(keys.csrf, host, nonce, at))
}
