package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/guard"
)

// Invisible visitor session of the secret proxy (change abuse-protection):
// the page solves a SHA-256 proof of work and exchanges it for a signed
// token bound to the site and the client network.
const (
	// SessionTTL is the lifetime of a session token.
	ProxySessionTTL = 30 * time.Minute
	// SessionBaseDifficulty is the secret counter bound of a normal
	// challenge (about 37 000 hashes on average: well under a second).
	SessionBaseDifficulty = 50_000
	// SessionHeader carries the token; PowHeader the fresh proof of work
	// of a "send" call.
	SessionHeader = "X-Dropsite-Session"
	PowHeader     = "X-Dropsite-Pow"

	sessionTokenPrefix = "v1."
	maxSessionToken    = 512
	sessionSweepEvery  = 1024
)

var (
	errSessionMissing = domain.E(domain.KindInvalid, domain.CodeProxySession,
		"this endpoint needs a visitor session: load /_dropsite/session.js and call dropsite.fetch")
	errSessionInvalid = domain.E(domain.KindInvalid, domain.CodeProxySession,
		"the visitor session is invalid or expired; get a new one")
	errSessionQuota = domain.E(domain.KindInvalid, domain.CodeProxySession,
		"the visitor session has used its calls; get a new one")
	errPowRequired = domain.E(domain.KindInvalid, domain.CodeProxyPow,
		"every call of this endpoint needs a fresh proof of work (dropsite.fetch does it)")
)

// SessionGrant is the answer to a solved session challenge.
type SessionGrant struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// Quota is the calls per endpoint the token allows at most.
	Quota int `json:"quota"`
}

type sessionClaims struct {
	Site string `json:"s"`
	Net  string `json:"n"`
	Exp  int64  `json:"e"`
	ID   string `json:"i"`
}

// sessionUses counts the calls of each token per endpoint until it expires.
type sessionUses struct {
	mu    sync.Mutex
	n     map[string]int
	exp   map[string]time.Time
	calls int
}

func newSessionUses() *sessionUses {
	return &sessionUses{n: map[string]int{}, exp: map[string]time.Time{}}
}

// take counts a call and reports whether it fits quota.
func (u *sessionUses) take(key string, quota int, exp, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	if u.calls%sessionSweepEvery == 0 {
		for k, e := range u.exp {
			if now.After(e) {
				delete(u.exp, k)
				delete(u.n, k)
			}
		}
	}
	if u.n[key] >= quota {
		return false
	}
	u.n[key]++
	u.exp[key] = exp
	return true
}

func (s *Service) sessionKey() []byte {
	if s.sealer == nil {
		return nil
	}
	return []byte(s.sealer.Derive("proxy-session-token"))
}

func (s *Service) signSession(c sessionClaims) (string, error) {
	key := s.sessionKey()
	if key == nil {
		return "", fmt.Errorf("session key is not loaded")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(sessionTokenPrefix + body))
	return sessionTokenPrefix + body + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

// verifySession checks the signature, expiry, site and network of a token.
func (s *Service) verifySession(token, siteID, ip string) (sessionClaims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return sessionClaims{}, errSessionMissing
	}
	key := s.sessionKey()
	rest, ok := strings.CutPrefix(token, sessionTokenPrefix)
	if !ok || key == nil || len(token) > maxSessionToken {
		return sessionClaims{}, errSessionInvalid
	}
	body, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return sessionClaims{}, errSessionInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return sessionClaims{}, errSessionInvalid
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(sessionTokenPrefix + body))
	if !hmac.Equal(got, m.Sum(nil)) {
		return sessionClaims{}, errSessionInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return sessionClaims{}, errSessionInvalid
	}
	var c sessionClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return sessionClaims{}, errSessionInvalid
	}
	if c.Site != siteID || c.Net != guard.NetKey(ip) || c.Net == "" || !s.now().Before(time.Unix(c.Exp, 0)) {
		return sessionClaims{}, errSessionInvalid
	}
	return c, nil
}

// SessionChallenge issues the proof-of-work challenge of a visitor session
// (purpose forms.PurposeSession) or of one "send" call (forms.PurposeSend).
// Suspicious networks get a harder one.
func (s *Service) SessionChallenge(ctx context.Context, rawHost, ip, purpose string) (altcha.Challenge, error) {
	if purpose != forms.PurposeSession && purpose != forms.PurposeSend {
		return altcha.Challenge{}, domain.ErrNotFound
	}
	_, host, err := s.siteForHost(ctx, rawHost)
	if err != nil {
		return altcha.Challenge{}, err
	}
	if err := s.checkNotBanned(ctx, ip); err != nil {
		return altcha.Challenge{}, err
	}
	if ok, retry := s.challengeIP.Allow(purpose + ":" + ipKey(ip)); !ok {
		return altcha.Challenge{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	n := SessionBaseDifficulty * guard.DifficultyFactor(s.suspicionLevel(ip))
	ch, err := s.altcha.Challenge(host, purpose, n, time.Now())
	if err != nil {
		return altcha.Challenge{}, fmt.Errorf("create session challenge: %w", err)
	}
	return ch, nil
}

// IssueSession exchanges a solved session challenge for a token.
func (s *Service) IssueSession(ctx context.Context, rawHost, ip, payload string) (SessionGrant, error) {
	site, host, err := s.siteForHost(ctx, rawHost)
	if err != nil {
		return SessionGrant{}, err
	}
	if err := s.checkNotBanned(ctx, ip); err != nil {
		return SessionGrant{}, err
	}
	net := guard.NetKey(ip)
	if net == "" {
		return SessionGrant{}, errSessionInvalid
	}
	fresh, err := s.spendAltcha(ctx, forms.PurposeSession, host, payload)
	if err != nil {
		return SessionGrant{}, err
	}
	if !fresh {
		return SessionGrant{}, errSessionInvalid
	}
	if s.suspicion.Session(net, s.now()) {
		s.flagNet(ctx, ip, "many sessions")
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return SessionGrant{}, fmt.Errorf("session id: %w", err)
	}
	exp := s.now().Add(ProxySessionTTL)
	tok, err := s.signSession(sessionClaims{Site: site.ID, Net: net, Exp: exp.Unix(), ID: hex.EncodeToString(id[:])})
	if err != nil {
		return SessionGrant{}, err
	}
	quota := proxyPolicies[domain.ProxyClassAI].SessionQuota
	if c := guard.QuotaCap(s.suspicionLevel(ip)); c > 0 {
		quota = min(quota, c)
	}
	return SessionGrant{Token: tok, ExpiresAt: time.Unix(exp.Unix(), 0).UTC(), Quota: quota}, nil
}

// useSession checks the token of a call to a templated endpoint and counts
// the call against the session quota of the endpoint.
func (s *Service) useSession(e domain.ProxyEndpoint, ip, token string) error {
	c, err := s.verifySession(token, e.SiteID, ip)
	if err != nil {
		return err
	}
	quota := ProxyEffectiveLimits(e.ProxyConfig).SessionQuota
	if capN := guard.QuotaCap(s.suspicionLevel(ip)); capN > 0 {
		quota = min(quota, capN)
	}
	if !s.sessions.take(fmt.Sprintf("%s|%d", c.ID, e.ID), quota, time.Unix(c.Exp, 0), s.now()) {
		return errSessionQuota
	}
	return nil
}

// usePow verifies and spends the fresh proof of work of a "send" call.
func (s *Service) usePow(ctx context.Context, host, payload string) error {
	if strings.TrimSpace(payload) == "" {
		return errPowRequired
	}
	fresh, err := s.spendAltcha(ctx, forms.PurposeSend, host, payload)
	if err != nil {
		return err
	}
	if !fresh {
		return errPowRequired
	}
	return nil
}
