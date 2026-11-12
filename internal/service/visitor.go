package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Visitor access to password-protected sites.
const (
	// VisitorCookieName is the host-only cookie that grants access to a site.
	// The __Host- prefix makes browsers refuse it unless it is Secure, has
	// Path=/ and no Domain, so a neighbouring site under the same base domain
	// cannot plant a cookie of this name (cookie tossing).
	VisitorCookieName = "__Host-" + brand.Name + "_access"
	VisitorCookieTTL  = 7 * 24 * time.Hour
	// Failed visitor logins per (IP, site): past the limit the source is
	// refused. IPv6 sources are counted per /64.
	VisitorFailuresPerIP  = 10
	VisitorFailuresWindow = 15 * time.Minute
	// VisitorFailuresPerSite failures on one site from all sources within
	// the window put the site under attack: logins are not refused, but every
	// login challenge is at least VisitorSiteAltchaLevel (base × 16).
	VisitorFailuresPerSite = 100
	VisitorSiteAltchaLevel = 2
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
	site, err := s.repo.FindSiteByHostname(ctx, host)
	if errors.Is(err, domain.ErrNotFound) {
		return s.siteOfPreview(ctx, host)
	}
	return site, err
}

// maxVisitorCookies bounds how many access cookie values one request may
// have checked.
const maxVisitorCookies = 8

// CheckVisitorAccess decides whether a request to host carrying the given
// access cookie values may be served: any valid one grants access, so a
// planted invalid cookie of the same name cannot shadow the real one.
// Errors must be treated as a refusal.
func (s *Service) CheckVisitorAccess(ctx context.Context, host string, cookies ...string) (AccessDecision, error) {
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
	cookies = slices.DeleteFunc(slices.Clone(cookies), func(c string) bool { return c == "" })
	if len(cookies) == 0 {
		return AccessLogin, nil
	}
	cookies = cookies[:min(len(cookies), maxVisitorCookies)]
	keys, err := s.visitorKeys(ctx)
	if err != nil {
		return AccessUnknown, err
	}
	acc, err := s.repo.GetSiteAccess(ctx, site.ID)
	if err != nil {
		return AccessUnknown, err
	}
	if acc.PasswordHash == "" {
		return AccessLogin, nil
	}
	for _, c := range cookies {
		if verifyCookie(keys.cookie, c, site.ID, acc.Version, s.now()) {
			return AccessAllow, nil
		}
	}
	return AccessLogin, nil
}

// VisitorSite returns the title of the protected site at host, for the login
// page. Unknown hosts and open sites are "not found".
func (s *Service) VisitorSite(ctx context.Context, host string) (string, error) {
	site, err := s.protectedSiteByHost(ctx, host)
	if err != nil {
		return "", err
	}
	return site.Title, nil
}

// VisitorCookie is a granted access cookie.
type VisitorCookie struct {
	Value   string
	Expires time.Time
}

var errVisitorPassword = domain.E(domain.KindUnauthorized, domain.CodeBadCredentials, "wrong password")

// ErrVisitorAltcha rejects a login without a valid unused Altcha solution.
// It reuses the anti-robot code of site forms: the meaning is the same.
var ErrVisitorAltcha = domain.E(domain.KindInvalid, domain.CodeFormCaptcha,
	"the browser check failed or expired; wait for it to finish and try again")

// visitorSource is the address a visitor's attempts are counted under: the
// full IPv4 address, or the /64 prefix of an IPv6 address (one subscriber
// usually holds a whole /64, so per-address keys would be free to rotate).
func visitorSource(ip string) string { return ipKey(ip) }

func visitorIPKey(ip, siteID string) string { return "visitor:" + siteID + ":" + visitorSource(ip) }

// beginVisitorLogin reserves one failure against the per-(IP, site) limit
// before the slow password check; the caller cancels it on success.
func (s *Service) beginVisitorLogin(siteID, ip string) (func(), func(), error) {
	release, ok := s.loginBusy.tryAcquire("visitor:" + visitorSource(ip))
	if !ok {
		return nil, nil, domain.ErrRateLimited.WithRetryAfter(LoginBusyRetryAfter)
	}
	ipRes, ok, retry := s.visitorIPLimit.Reserve(visitorIPKey(ip, siteID))
	if !ok {
		release()
		return nil, nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	return release, ipRes.Cancel, nil
}

// protectedSiteByHost resolves host to a protected site; anything else is
// "not found".
func (s *Service) protectedSiteByHost(ctx context.Context, host string) (domain.Site, error) {
	site, err := s.siteByHost(ctx, host)
	if err != nil {
		return domain.Site{}, err
	}
	if !site.Protected {
		return domain.Site{}, domain.ErrNotFound
	}
	return site, nil
}

// VisitorAltchaChallenge issues the proof-of-work challenge of the login page
// of the protected site at host. It is signed for the login purpose, so it
// cannot be spent on the site's forms and a form challenge cannot be spent
// here. Difficulty escalates per (IP, site) with every challenge and every
// failed login (see issueAltcha); while the site is under attack (the
// per-site failure threshold is reached) every visitor gets at least
// VisitorSiteAltchaLevel.
func (s *Service) VisitorAltchaChallenge(ctx context.Context, host, ip string) (altcha.Challenge, error) {
	site, err := s.protectedSiteByHost(ctx, host)
	if err != nil {
		return altcha.Challenge{}, err
	}
	floor := 0
	if ok, _ := s.visitorSiteLimit.Check(site.ID); !ok {
		floor = VisitorSiteAltchaLevel
	}
	return s.issueAltcha(ctx, forms.PurposeLogin, site.ID, host, visitorSource(ip), floor)
}

// VisitorLoginInput is a submitted login form.
type VisitorLoginInput struct {
	Host     string // request host without port
	Password string
	Altcha   string // the widget payload (base64 JSON)
	IP       string
}

// VisitorLogin checks the Altcha solution, then the site password, and
// returns an access cookie.
//
//   - An exhausted per-(IP, site) limit refuses at once (429), before any
//     proof of work is spent; the password is not checked.
//   - Without a valid unused solution the password is not checked and
//     nothing counts toward the failure limits.
//   - The per-site failure count never refuses a login: past its threshold
//     it raises the Altcha difficulty for the whole site instead, so nobody
//     can lock the owner's visitors out.
//
// Failures raise the Altcha difficulty for the source.
func (s *Service) VisitorLogin(ctx context.Context, in VisitorLoginInput) (VisitorCookie, error) {
	site, err := s.protectedSiteByHost(ctx, in.Host)
	if err != nil {
		return VisitorCookie{}, err
	}
	src := visitorSource(in.IP)
	if ok, retry := s.visitorIPLimit.Check(visitorIPKey(src, site.ID)); !ok {
		return VisitorCookie{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	fresh, err := s.spendAltcha(ctx, forms.PurposeLogin, in.Host, in.Altcha)
	if err != nil {
		return VisitorCookie{}, err
	}
	if !fresh {
		s.penalizeAltcha(forms.PurposeLogin, src, site.ID)
		return VisitorCookie{}, ErrVisitorAltcha
	}
	c, err := s.checkVisitorPassword(ctx, site, in.Password, src)
	if errors.Is(err, errVisitorPassword) {
		s.penalizeAltcha(forms.PurposeLogin, src, site.ID)
		s.visitorSiteLimit.Record(site.ID)
	}
	return c, err
}

func (s *Service) checkVisitorPassword(ctx context.Context, site domain.Site, password, ip string) (VisitorCookie, error) {
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
