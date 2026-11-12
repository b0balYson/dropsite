package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Shared workspaces (change shared-workspaces).

func (a *api) workspaceRoutes() []route {
	return []route{
		{Method: http.MethodGet, Path: "/workspaces", handle: a.listWorkspaces},
		{Method: http.MethodPost, Path: "/workspaces", handle: a.createWorkspace},
		{Method: http.MethodGet, Path: "/workspaces/{id}", handle: a.getWorkspace},
		{Method: http.MethodPatch, Path: "/workspaces/{id}", handle: a.renameWorkspace},
		{Method: http.MethodDelete, Path: "/workspaces/{id}", handle: a.deleteWorkspace},
		{Method: http.MethodPost, Path: "/workspaces/{id}/members", handle: a.addWorkspaceMember},
		{Method: http.MethodDelete, Path: "/workspaces/{id}/members/{user_id}", handle: a.removeWorkspaceMember},
		{Method: http.MethodPost, Path: "/sites/{ref}/move", handle: a.moveSite},
	}
}

type workspaceMemberDTO struct {
	UserID  string    `json:"user_id"`
	Email   string    `json:"email"`
	AddedAt time.Time `json:"added_at"`
}

type workspaceDTO struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	MemberCount int                  `json:"member_count"`
	SiteCount   int                  `json:"site_count"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Members     []workspaceMemberDTO `json:"members,omitempty"`
}

func toWorkspace(w domain.Workspace) workspaceDTO {
	return workspaceDTO{
		ID: idString(w.ID), Name: w.Name, MemberCount: w.MemberCount, SiteCount: w.SiteCount,
		CreatedAt: utc(w.CreatedAt), UpdatedAt: utc(w.UpdatedAt),
	}
}

func toWorkspaceView(v service.WorkspaceView) workspaceDTO {
	out := toWorkspace(v.Workspace)
	out.Members = make([]workspaceMemberDTO, 0, len(v.Members))
	for _, m := range v.Members {
		out.Members = append(out.Members, workspaceMemberDTO{UserID: idString(m.UserID), Email: m.Email, AddedAt: utc(m.AddedAt)})
	}
	return out
}

// workspaceParam parses the {id} path value; anything else is not found.
func workspaceParam(r *http.Request) (int64, error) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		return 0, domain.ErrNotFound
	}
	return id, nil
}

// workspaceField parses an optional workspace_id body field ("" or absent:
// personal).
func workspaceField(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	id, ok := parseID(v)
	if !ok {
		return 0, invalidField("workspace_id", "workspace_id must be a workspace ID")
	}
	return id, nil
}

// siteScope parses the workspace filter of the site list.
func siteScope(v string) (service.SiteScope, error) {
	switch v {
	case "":
		return service.SiteScope{}, nil
	case "personal":
		return service.PersonalScope(), nil
	}
	id, ok := parseID(v)
	if !ok {
		return service.SiteScope{}, invalidField("workspace", `workspace must be "personal" or a workspace ID`)
	}
	return service.WorkspaceScope(id), nil
}

func (a *api) listWorkspaces(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.svc.ListWorkspaces(r.Context(), actorFrom(r.Context()), r.URL.Query().Get("all") == "true")
	if err != nil {
		return err
	}
	out := make([]workspaceDTO, 0, len(rows))
	for _, ws := range rows {
		out = append(out, toWorkspace(ws))
	}
	writeJSON(w, http.StatusOK, newList(out, len(out)))
	return nil
}

type workspaceRequest struct {
	Name string `json:"name"`
}

func (a *api) createWorkspace(w http.ResponseWriter, r *http.Request) error {
	var req workspaceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := a.svc.CreateWorkspace(r.Context(), actorFrom(r.Context()), req.Name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toWorkspaceView(v))
	return nil
}

func (a *api) getWorkspace(w http.ResponseWriter, r *http.Request) error {
	id, err := workspaceParam(r)
	if err != nil {
		return err
	}
	v, err := a.svc.GetWorkspace(r.Context(), actorFrom(r.Context()), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toWorkspaceView(v))
	return nil
}

func (a *api) renameWorkspace(w http.ResponseWriter, r *http.Request) error {
	id, err := workspaceParam(r)
	if err != nil {
		return err
	}
	var req workspaceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := a.svc.RenameWorkspace(r.Context(), actorFrom(r.Context()), id, req.Name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toWorkspaceView(v))
	return nil
}

func (a *api) deleteWorkspace(w http.ResponseWriter, r *http.Request) error {
	id, err := workspaceParam(r)
	if err != nil {
		return err
	}
	if err := a.svc.DeleteWorkspace(r.Context(), actorFrom(r.Context()), id); err != nil {
		return err
	}
	noContent(w)
	return nil
}

type memberRequest struct {
	Email string `json:"email"`
}

func (a *api) addWorkspaceMember(w http.ResponseWriter, r *http.Request) error {
	id, err := workspaceParam(r)
	if err != nil {
		return err
	}
	var req memberRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	v, err := a.svc.AddWorkspaceMember(r.Context(), actorFrom(r.Context()), id, req.Email)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toWorkspaceView(v))
	return nil
}

func (a *api) removeWorkspaceMember(w http.ResponseWriter, r *http.Request) error {
	id, err := workspaceParam(r)
	if err != nil {
		return err
	}
	userID, ok := parseID(r.PathValue("user_id"))
	if !ok {
		return domain.ErrNotFound
	}
	if _, err := a.svc.RemoveWorkspaceMember(r.Context(), actorFrom(r.Context()), id, userID); err != nil {
		return err
	}
	noContent(w)
	return nil
}

type moveRequest struct {
	WorkspaceID json.RawMessage `json:"workspace_id"`
}

func (a *api) moveSite(w http.ResponseWriter, r *http.Request) error {
	var req moveRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.WorkspaceID == nil {
		return invalidField("workspace_id", "workspace_id is required: a workspace ID, or null for personal")
	}
	raw := ""
	if !bytes.Equal(req.WorkspaceID, jsonNull) && json.Unmarshal(req.WorkspaceID, &raw) != nil {
		return invalidField("workspace_id", "workspace_id must be a string or null")
	}
	ws, err := workspaceField(raw)
	if err != nil {
		return err
	}
	v, err := a.svc.MoveSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), ws)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}
