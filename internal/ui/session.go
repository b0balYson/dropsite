package ui

import (
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

const (
	sessionCookieName = "ds_session"
	langCookieName    = "ds_lang"
	langCookieMaxAge  = 365 * 24 * time.Hour
)

func (s *server) sessionCookie(r *http.Request) string {
	c, err := s.readCookie(r, sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// cookieName is the name a cookie has on this listener. On the public
// listener every cookie carries the __Host- prefix: the admin host lies
// under the base domain, whose other subdomains run user JavaScript, and a
// browser accepts a __Host- cookie only from the host itself (Secure,
// Path=/, no Domain), so a published site cannot plant or overwrite one.
func (s *server) cookieName(name string) string {
	if s.public != nil {
		return "__Host-" + name
	}
	return name
}

// readCookie reads the cookie of this listener; on the public listener a
// cookie without the prefix (set by a sibling subdomain) is never read.
func (s *server) readCookie(r *http.Request, name string) (*http.Cookie, error) {
	return r.Cookie(s.cookieName(name))
}

// isSecure reports whether cookies may carry the Secure attribute: the
// interface normally runs over plain HTTP on a private address, where
// browsers would drop Secure cookies.
func (s *server) isSecure(r *http.Request) bool {
	return r.TLS != nil || s.secure || s.public != nil
}

// cookie builds a host-only, HttpOnly, SameSite=Lax cookie.
func (s *server) cookie(r *http.Request, name, value string, maxAge time.Duration) *http.Cookie {
	// Secure is set only over HTTPS: on the plain-HTTP admin listener the
	// browser would drop the cookie (design.md, section 5).
	c := &http.Cookie{ //nolint:gosec // G124: HttpOnly and SameSite are always set; Secure follows the transport
		Name: s.cookieName(name), Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.isSecure(r),
	}
	switch {
	case maxAge > 0:
		c.MaxAge = int(maxAge.Seconds())
	case maxAge < 0:
		c.MaxAge = -1
	}
	return c
}

func (s *server) setSession(w http.ResponseWriter, r *http.Request, secret string) {
	http.SetCookie(w, s.cookie(r, sessionCookieName, secret, service.SessionTTL))
}

func (s *server) clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, s.cookie(r, sessionCookieName, "", -1))
}

func (s *server) setLangCookie(w http.ResponseWriter, r *http.Request, lang string) {
	http.SetCookie(w, s.cookie(r, langCookieName, lang, langCookieMaxAge))
}

// openSession logs a user in after a password check or invite redemption.
func (s *server) openSession(w http.ResponseWriter, r *http.Request, res service.LoginResult) {
	s.setSession(w, r, res.Secret)
	if c, err := s.readCookie(r, langCookieName); err == nil && supportedLang(c.Value) && res.User.Lang != c.Value {
		actor := service.Actor{User: res.User, IP: s.clientIP(r)}
		if err := s.svc.SetLang(r.Context(), actor, c.Value); err != nil {
			s.log.Warn("store language failed", "err", err)
		}
	}
}
