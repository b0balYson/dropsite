package ui

import (
	"errors"
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/oauth"
	"github.com/b0balYson/dropsite/internal/service"
)

// sessionAuthorizePath approves an OAuth request on the public admin host
// for the signed-in user, instead of a connect code. The consent page of the
// API host links here (with the request it sealed) while public access is
// on; signing in first goes through the usual login with Altcha.
const sessionAuthorizePath = "/connect/authorize"

// sessionPending opens the sealed request of the API host's consent page.
// The request never carries a connect code here.
func (s *server) sessionPending(sealed string) (oauth.Pending, bool) {
	if s.az == nil {
		return oauth.Pending{}, false
	}
	p, err := s.az.Open(sealed)
	if err != nil {
		return oauth.Pending{}, false
	}
	p.LinkCode = ""
	return p, true
}

func (s *server) sessionConsent(rc *reqCtx, p oauth.Pending) authorizeData {
	d := consentData(s.az, p)
	d.Action = sessionAuthorizePath
	d.SessionEmail = rc.actor.User.Email
	return d
}

func (s *server) sessionAuthorizePage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if s.az == nil {
		s.notFound(w, r, rc)
		return
	}
	p, ok := s.sessionPending(r.URL.Query().Get("request"))
	if !ok {
		s.renderConsent(w, r, rc, http.StatusBadRequest, authorizeData{Fatal: rc.loc.T("oauth.error.form")}, "")
		return
	}
	s.renderConsent(w, r, rc, http.StatusOK, s.sessionConsent(rc, p), p.Request.RedirectURI)
}

func (s *server) sessionAuthorizeSubmit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if s.az == nil {
		s.notFound(w, r, rc)
		return
	}
	p, ok := s.sessionPending(r.PostFormValue("request"))
	if !ok {
		s.renderConsent(w, r, rc, http.StatusBadRequest, authorizeData{Fatal: rc.loc.T("oauth.error.form")}, "")
		return
	}
	if r.PostFormValue("action") == "deny" {
		consentRedirect(w, r, p, s.az.ErrorURL(&oauth.RedirectError{
			RedirectURI: p.Request.RedirectURI, State: p.State, Code: "access_denied", Description: "the user denied access",
		}))
		return
	}
	actor := rc.actor
	actor.IP = rc.ip
	code, err := s.svc.AuthorizeOAuthSession(r.Context(), actor, p.Request)
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		consentRedirect(w, r, p, s.az.ErrorURL(&oauth.RedirectError{
			RedirectURI: p.Request.RedirectURI, State: p.State, Code: oe.Code, Description: oe.Description,
		}))
		return
	}
	if err != nil {
		d := s.sessionConsent(rc, p)
		msg, status := s.describe(rc, err)
		if domain.AsError(err).Kind == domain.KindRateLimited {
			w.Header().Set("Retry-After", retryAfter(err))
		}
		d.Error = msg
		s.renderConsent(w, r, rc, status, d, p.Request.RedirectURI)
		return
	}
	consentRedirect(w, r, p, s.az.SuccessURL(p, code))
}
