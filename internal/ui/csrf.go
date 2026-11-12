package ui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
)

const (
	csrfField      = "_csrf"
	csrfHeader     = "X-CSRF-Token"
	anonCookieName = "ds_csrf"
	anonTokenBytes = 32
)

// sessionCSRF derives the form token from the session's CSRF secret.
func sessionCSRF(secret []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("csrf"))
	return hex.EncodeToString(m.Sum(nil))
}

// anonCSRF returns the double-submit token for pages without a session,
// issuing the cookie when it is missing.
func (s *server) anonCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(anonCookieName); err == nil && len(c.Value) == 2*anonTokenBytes {
		if _, err := hex.DecodeString(c.Value); err == nil {
			return c.Value
		}
	}
	b := make([]byte, anonTokenBytes)
	if _, err := rand.Read(b); err != nil {
		s.log.Error("generate csrf token", "err", err)
		return ""
	}
	v := hex.EncodeToString(b)
	http.SetCookie(w, s.cookie(r, anonCookieName, v, 0))
	return v
}

// tokensEqual compares tokens in constant time; an empty token never matches.
func tokensEqual(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// sameOrigin rejects cross-site requests using the browser's fetch metadata
// and Origin headers. Clients that send neither (old browsers, curl) still
// need the CSRF token.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == r.Host
}
