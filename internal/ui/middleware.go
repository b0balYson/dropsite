package ui

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// maxFormBytes bounds url-encoded form bodies; uploads have their own limit.
const maxFormBytes = 1 << 20

// reqCtx is the per-request state shared by handlers and templates.
type reqCtx struct {
	actor   service.Actor
	session domain.Session
	secret  string
	authed  bool
	loc     *locale
	csrf    string
	ip      string
	// csrfChecked is false only for upload requests whose token travels in
	// the multipart body; the handler must verify it before reading the file.
	csrfChecked bool
}

type handler func(w http.ResponseWriter, r *http.Request, rc *reqCtx)

// anon serves pages that work without a session (login, invite, language).
func (s *server) anon(h handler) http.HandlerFunc {
	return s.wrap(h, false, false, true)
}

// user requires a session and redirects to the login page otherwise.
func (s *server) user(h handler) http.HandlerFunc {
	return s.wrap(h, true, false, true)
}

// admin requires an administrator; anyone else gets the regular 404 page.
func (s *server) admin(h handler) http.HandlerFunc {
	return s.wrap(h, true, true, true)
}

// upload is user() for multipart uploads: the CSRF token may arrive in the
// body, so the body is left untouched for the handler to stream.
func (s *server) upload(h handler) http.HandlerFunc {
	return s.wrap(h, true, false, false)
}

func (s *server) wrap(h handler, needUser, needAdmin, checkBody bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc := s.begin(w, r)
		if needUser && !rc.authed {
			s.toLogin(w, r)
			return
		}
		if needAdmin && !rc.actor.IsAdmin() {
			s.notFound(w, r, rc)
			return
		}
		if isUnsafe(r.Method) {
			if !sameOrigin(r) {
				s.forbidden(w, r, rc)
				return
			}
			rc.csrfChecked = s.csrfFromRequest(w, r, rc, checkBody)
			if checkBody && !rc.csrfChecked {
				s.forbidden(w, r, rc)
				return
			}
		}
		h(w, r, rc)
	}
}

// begin resolves the session, language and CSRF token for a request.
func (s *server) begin(w http.ResponseWriter, r *http.Request) *reqCtx {
	rc := &reqCtx{ip: clientIP(r)}
	if secret := sessionCookie(r); secret != "" {
		actor, sess, err := s.svc.AuthenticateSession(r.Context(), secret, rc.ip)
		if err == nil {
			rc.actor, rc.session, rc.secret, rc.authed = actor, sess, secret, true
		} else {
			s.clearSession(w, r)
		}
	}
	rc.loc = s.cats.locale(s.pickLang(r, rc))
	if rc.authed {
		rc.csrf = sessionCSRF(rc.session.CSRFSecret)
	} else {
		rc.csrf = s.anonCSRF(w, r)
	}
	return rc
}

// csrfFromRequest checks the header token or, for url-encoded forms, the
// _csrf field. It never parses multipart bodies.
func (s *server) csrfFromRequest(w http.ResponseWriter, r *http.Request, rc *reqCtx, readBody bool) bool {
	if h := r.Header.Get(csrfHeader); h != "" {
		return tokensEqual(rc.csrf, h)
	}
	if !readBody {
		return false
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return tokensEqual(rc.csrf, r.PostFormValue(csrfField))
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// clientIP is the connection address: the UI listener is reached directly,
// with no proxy in front, so X-Forwarded-For is never read here.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) toLogin(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if p := r.URL.RequestURI(); p != "/" && r.Method == http.MethodGet {
		target += "?next=" + url.QueryEscape(p)
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// safeNext accepts only same-site relative paths, guarding against open
// redirects such as //evil.example or /\evil.example.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.ContainsAny(next, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return next
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
