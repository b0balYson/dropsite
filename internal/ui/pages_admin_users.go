package ui

import (
	"net/http"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type userForm struct {
	Email string
	Role  string
	Error string
}

type usersData struct {
	Users  []service.UserSummary
	Query  string
	Pager  pager
	Invite *service.InviteLink
	Form   userForm
	MeID   int64
}

func (s *server) usersPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderUsers(w, r, rc, http.StatusOK, usersData{})
}

func (s *server) renderUsers(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, data usersData) {
	n, page := pageParam(r)
	data.Query = r.URL.Query().Get("q")
	users, total, err := s.svc.ListUsers(r.Context(), rc.actor, service.UserFilter{Query: data.Query, Page: page})
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data.Users, data.MeID = users, rc.actor.User.ID
	data.Pager = newPager("/admin/users", r.URL.Query(), n, total)
	s.page(w, r, rc, status, "admin_users", "users", "users.title", data)
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	form := userForm{Email: r.PostFormValue("email"), Role: r.PostFormValue("role")}
	link, err := s.svc.CreateUser(r.Context(), rc.actor, form.Email, domain.Role(form.Role))
	if err != nil {
		var status int
		form.Error, status = s.describe(rc, err)
		s.renderUsers(w, r, rc, status, usersData{Form: form})
		return
	}
	s.renderUsers(w, r, rc, http.StatusOK, usersData{Invite: &link})
}

func (s *server) reinviteUser(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, rc)
		return
	}
	link, err := s.svc.ReissueInvite(r.Context(), rc.actor, id)
	if err != nil {
		s.fail(w, r, rc, err, "/admin/users")
		return
	}
	s.renderUsers(w, r, rc, http.StatusOK, usersData{Invite: &link})
}

func (s *server) blockUser(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.setBlocked(w, r, rc, true)
}

func (s *server) unblockUser(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.setBlocked(w, r, rc, false)
}

func (s *server) setBlocked(w http.ResponseWriter, r *http.Request, rc *reqCtx, blocked bool) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, rc)
		return
	}
	u, err := s.svc.SetUserBlocked(r.Context(), rc.actor, id, blocked)
	if err != nil {
		s.fail(w, r, rc, err, "/admin/users")
		return
	}
	key := "users.unblocked"
	if blocked {
		key = "users.blocked"
	}
	s.setFlash(w, r, flashOK, rc.loc.T(key, "email", u.Email))
	redirect(w, r, "/admin/users")
}

func (s *server) revokeUserTokens(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, rc)
		return
	}
	n, err := s.svc.RevokeUserTokens(r.Context(), rc.actor, id)
	if err != nil {
		s.fail(w, r, rc, err, "/admin/users")
		return
	}
	u, err := s.svc.GetUser(r.Context(), rc.actor, id)
	if err != nil {
		s.fail(w, r, rc, err, "/admin/users")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("users.tokens_revoked", "email", u.Email, "count", strconv.Itoa(n)))
	redirect(w, r, "/admin/users")
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
