package ui

import (
	"net/http"

	"github.com/b0balYson/dropsite/internal/domain"
)

// publicAdminSection is the id of the settings section, the target of its
// form results.
const publicAdminSection = "public-admin-h"

// savePublicAdmin turns public access to the admin interface on (with the
// confirmation checkbox and the administrator's password) or off.
func (s *server) savePublicAdmin(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	back := "/admin/settings#" + publicAdminSection
	on := r.PostFormValue("enabled") == "1"
	err := s.svc.SetPublicAdmin(r.Context(), rc.actor, on, r.PostFormValue("confirm") == "1", r.PostFormValue("password"))
	if err != nil {
		switch domain.AsError(err).Code {
		case domain.CodeInvalidInput:
			s.setFlash(w, r, flashError, rc.loc.T("publicadmin.need_confirm"))
		case domain.CodeBadCredentials:
			s.setFlash(w, r, flashError, rc.loc.T("publicadmin.bad_password"))
		default:
			s.fail(w, r, rc, err, back)
			return
		}
		redirect(w, r, back)
		return
	}
	msg := "publicadmin.disabled"
	if on {
		msg = "publicadmin.enabled"
	}
	s.setFlash(w, r, flashOK, rc.loc.T(msg, "url", s.svc.PublicAdminURL()))
	redirect(w, r, back)
}

// publicAdminURL is the public address of the interface while public access
// is on, for the notes on the checklist and the Connect AI page; "" when off.
func (s *server) publicAdminURL(r *http.Request) string {
	if !s.svc.PublicAdminEnabled(r.Context()) {
		return ""
	}
	return s.svc.PublicAdminURL()
}
