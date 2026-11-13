package ui

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type loginData struct {
	Email string
	Next  string
	Error string
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	next := safeNext(r.URL.Query().Get("next"))
	if rc.authed {
		redirect(w, r, next)
		return
	}
	s.authPage(w, r, rc, http.StatusOK, "login", "login.title", loginData{Next: next})
}

func (s *server) loginSubmit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	email, next := r.PostFormValue("email"), safeNext(r.PostFormValue("next"))
	res, err := s.svc.LoginWithAltcha(r.Context(), service.LoginInput{
		Email: email, Password: r.PostFormValue("password"), Altcha: r.PostFormValue(altchaField),
		Host: r.Host, IP: rc.ip, UserAgent: r.UserAgent(),
	})
	if err != nil {
		msg, status := s.describeAuth(rc, err)
		s.authPage(w, r, rc, status, "login", "login.title", loginData{Email: email, Next: next, Error: msg})
		return
	}
	s.openSession(w, r, res)
	redirect(w, r, next)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.svc.Logout(r.Context(), rc.secret); err != nil {
		s.log.Error("logout failed", "err", err)
	}
	s.clearSession(w, r)
	redirect(w, r, "/login")
}

type inviteData struct {
	Secret  string
	Email   string
	Invalid bool
	Error   string
}

func (s *server) invitePage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	secret := r.PathValue("secret")
	u, err := s.svc.InviteUser(r.Context(), secret)
	if err != nil {
		s.authPage(w, r, rc, http.StatusNotFound, "invite", "invite.title", inviteData{Invalid: true})
		return
	}
	s.authPage(w, r, rc, http.StatusOK, "invite", "invite.title", inviteData{Secret: secret, Email: u.Email})
}

func (s *server) inviteSubmit(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	secret := r.PathValue("secret")
	u, err := s.svc.InviteUser(r.Context(), secret)
	if err != nil {
		s.authPage(w, r, rc, http.StatusNotFound, "invite", "invite.title", inviteData{Invalid: true})
		return
	}
	data := inviteData{Secret: secret, Email: u.Email}
	if err := s.svc.CheckAdminAltcha(r.Context(), r.Host, r.PostFormValue(altchaField), rc.ip); err != nil {
		data.Error, _ = s.describeAuth(rc, err)
		s.authPage(w, r, rc, http.StatusBadRequest, "invite", "invite.title", data)
		return
	}
	password := r.PostFormValue("password")
	if password != r.PostFormValue("password2") {
		data.Error = rc.loc.T("invite.mismatch")
		s.authPage(w, r, rc, http.StatusUnprocessableEntity, "invite", "invite.title", data)
		return
	}
	user, err := s.svc.RedeemInvite(r.Context(), secret, password, rc.ip)
	if err != nil {
		if domain.AsError(err).Code == domain.CodeInviteInvalid {
			s.authPage(w, r, rc, http.StatusNotFound, "invite", "invite.title", inviteData{Invalid: true})
			return
		}
		data.Error, _ = s.describe(rc, err)
		s.authPage(w, r, rc, http.StatusUnprocessableEntity, "invite", "invite.title", data)
		return
	}
	res, err := s.svc.Login(r.Context(), user.Email, password, rc.ip, r.UserAgent())
	if err != nil {
		// The password is set; the user can still sign in by hand.
		s.log.Warn("login after invite failed", "err", err)
		redirect(w, r, "/login")
		return
	}
	s.openSession(w, r, res)
	redirect(w, r, "/")
}

// describeAuth is describe with the Altcha failure of the sign-in forms.
func (s *server) describeAuth(rc *reqCtx, err error) (string, int) {
	if domain.AsError(err).Code == domain.CodeFormCaptcha {
		return rc.loc.T("login.altcha"), http.StatusBadRequest
	}
	return s.describe(rc, err)
}

func (s *server) setLang(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	lang := r.PostFormValue("lang")
	if supportedLang(lang) {
		s.setLangCookie(w, r, lang)
		if rc.authed {
			if err := s.svc.SetLang(r.Context(), rc.actor, lang); err != nil {
				s.log.Error("store language failed", "err", err)
			}
		}
	}
	redirect(w, r, safeNext(r.PostFormValue("next")))
}
