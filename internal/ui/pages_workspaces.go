package ui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Shared workspaces (change shared-workspaces).

func (s *server) workspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspaces", s.user(s.workspacesPage))
	mux.HandleFunc("POST /workspaces", s.user(s.createWorkspace))
	mux.HandleFunc("GET /workspaces/{id}", s.user(s.workspacePage))
	mux.HandleFunc("POST /workspaces/{id}/rename", s.user(s.renameWorkspace))
	mux.HandleFunc("POST /workspaces/{id}/members", s.user(s.addWorkspaceMember))
	mux.HandleFunc("POST /workspaces/{id}/members/{user}/remove", s.user(s.removeWorkspaceMember))
	mux.HandleFunc("POST /workspaces/{id}/delete", s.user(s.deleteWorkspace))
	mux.HandleFunc("POST /sites/{ref}/move", s.user(s.moveSite))
}

type workspacesData struct {
	Workspaces []domain.Workspace
	Name       string
	Error      string
	All        bool
}

type workspaceData struct {
	W    service.WorkspaceView
	MeID int64
}

// siteWorkspace is the workspace block of the site page.
type siteWorkspace struct {
	ID      int64 // 0: personal
	Name    string
	CanMove bool
	Options []domain.Workspace
}

// sitesFilter is the workspace filter of "My sites".
type sitesFilter struct {
	Value      string // "", "personal" or a workspace ID
	Workspaces []domain.Workspace
}

func workspacePath(id int64) string { return "/workspaces/" + strconv.FormatInt(id, 10) }

// pathWorkspace parses the {id} path value; anything else is not found.
func pathWorkspace(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.ErrNotFound
	}
	return id, nil
}

// parseSitesFilter reads ?workspace= of "My sites".
func parseSitesFilter(v string) (service.SiteScope, string) {
	if v == "personal" {
		return service.PersonalScope(), v
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
		return service.WorkspaceScope(id), v
	}
	return service.SiteScope{}, ""
}

// formWorkspace reads an optional workspace field of a form (0: personal).
func formWorkspace(r *http.Request) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("workspace")), 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

func (s *server) workspacesPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderWorkspaces(w, r, rc, http.StatusOK, workspacesData{})
}

func (s *server) renderWorkspaces(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, data workspacesData) {
	data.All = rc.actor.IsAdmin()
	list, err := s.svc.ListWorkspaces(r.Context(), rc.actor, data.All)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data.Workspaces = list
	s.page(w, r, rc, status, "workspaces", "workspaces", "workspaces.title", data)
}

func (s *server) createWorkspace(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	name := r.PostFormValue("name")
	v, err := s.svc.CreateWorkspace(r.Context(), rc.actor, name)
	if err != nil {
		msg, status := s.describe(rc, err)
		s.renderWorkspaces(w, r, rc, status, workspacesData{Name: name, Error: msg})
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("workspaces.created"))
	redirect(w, r, workspacePath(v.ID))
}

func (s *server) workspacePage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := pathWorkspace(r)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	v, err := s.svc.GetWorkspace(r.Context(), rc.actor, id)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.page(w, r, rc, http.StatusOK, "workspace", "workspaces", "", workspaceData{W: v, MeID: rc.actor.User.ID})
}

func (s *server) renameWorkspace(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := pathWorkspace(r)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if _, err := s.svc.RenameWorkspace(r.Context(), rc.actor, id, r.PostFormValue("name")); err != nil {
		s.fail(w, r, rc, err, workspacePath(id))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("workspaces.renamed"))
	redirect(w, r, workspacePath(id))
}

func (s *server) addWorkspaceMember(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := pathWorkspace(r)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	if _, err := s.svc.AddWorkspaceMember(r.Context(), rc.actor, id, email); err != nil {
		s.fail(w, r, rc, err, workspacePath(id))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("workspaces.member.added", "email", email))
	redirect(w, r, workspacePath(id))
}

func (s *server) removeWorkspaceMember(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := pathWorkspace(r)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil || userID <= 0 {
		s.notFound(w, r, rc)
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.setFlash(w, r, flashError, rc.loc.T("workspaces.unconfirmed"))
		redirect(w, r, workspacePath(id))
		return
	}
	deleted, err := s.svc.RemoveWorkspaceMember(r.Context(), rc.actor, id, userID)
	if err != nil {
		s.fail(w, r, rc, err, workspacePath(id))
		return
	}
	switch {
	case deleted:
		s.setFlash(w, r, flashOK, rc.loc.T("workspaces.deleted"))
		redirect(w, r, "/workspaces")
	case userID == rc.actor.User.ID && !rc.actor.IsAdmin():
		s.setFlash(w, r, flashOK, rc.loc.T("workspaces.left"))
		redirect(w, r, "/workspaces")
	default:
		s.setFlash(w, r, flashOK, rc.loc.T("workspaces.member.removed"))
		redirect(w, r, workspacePath(id))
	}
}

func (s *server) deleteWorkspace(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := pathWorkspace(r)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.setFlash(w, r, flashError, rc.loc.T("workspaces.unconfirmed"))
		redirect(w, r, workspacePath(id))
		return
	}
	if err := s.svc.DeleteWorkspace(r.Context(), rc.actor, id); err != nil {
		s.fail(w, r, rc, err, workspacePath(id))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("workspaces.deleted"))
	redirect(w, r, "/workspaces")
}

func (s *server) moveSite(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	back := sectionPath(ref, secSettings)
	if r.PostFormValue("confirm") != "yes" {
		s.setFlash(w, r, flashError, rc.loc.T("workspaces.unconfirmed"))
		redirect(w, r, back)
		return
	}
	v, err := s.svc.MoveSite(r.Context(), rc.actor, ref, formWorkspace(r))
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	if v.WorkspaceName != "" {
		s.setFlash(w, r, flashOK, rc.loc.T("workspaces.moved", "name", v.WorkspaceName))
	} else {
		s.setFlash(w, r, flashOK, rc.loc.T("workspaces.moved.personal"))
	}
	redirect(w, r, sectionPath(v.Site.ID, secSettings))
}

// siteWorkspaceView builds the workspace block of a site page.
func (s *server) siteWorkspaceView(r *http.Request, rc *reqCtx, v service.SiteView) (siteWorkspace, error) {
	out := siteWorkspace{Name: v.WorkspaceName, CanMove: rc.actor.IsAdmin() || v.OwnerID == rc.actor.User.ID}
	if v.Site.WorkspaceID != nil {
		out.ID = *v.Site.WorkspaceID
	}
	if !out.CanMove {
		return out, nil
	}
	list, err := s.svc.ListWorkspaces(r.Context(), rc.actor, rc.actor.IsAdmin())
	if err != nil {
		return siteWorkspace{}, err
	}
	out.Options = list
	return out, nil
}
