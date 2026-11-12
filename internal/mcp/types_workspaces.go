package mcp

import (
	"context"
	"errors"
	"strings"
)

// NotFoundWorkspace is the not-found text of the workspace tools.
const NotFoundWorkspace = "not found: no such workspace (or member, or site) for this user; " +
	"call list_workspaces and list_sites to see valid IDs and e-mails"

// Workspace is a shared workspace as tools return it.
type Workspace struct {
	ID        string   `json:"workspace_id" jsonschema:"pass it as workspace to the other tools"`
	Name      string   `json:"name"`
	Members   []string `json:"members" jsonschema:"e-mails of the members; all members have the same rights"`
	SiteCount int      `json:"site_count"`
}

// MemberRemoval is the outcome of remove_workspace_member.
type MemberRemoval struct {
	Removed          string `json:"removed" jsonschema:"e-mail of the removed member"`
	WorkspaceDeleted bool   `json:"workspace_deleted" jsonschema:"true when the last member left an empty workspace, which is deleted with it"`
}

// WorkspaceBackend manages shared workspaces and creates or moves sites in
// them. The tools fail when the backend does not implement it.
type WorkspaceBackend interface {
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
	CreateWorkspace(ctx context.Context, name string) (Workspace, error)
	AddWorkspaceMember(ctx context.Context, workspace, email string) (Workspace, error)
	RemoveWorkspaceMember(ctx context.Context, workspace, email string) (MemberRemoval, error)
	MoveSite(ctx context.Context, site, workspace string) (Site, error)
	CreateSiteIn(ctx context.Context, workspace, title, slug string) (Site, error)
	CloneSiteIn(ctx context.Context, workspace, ref, title, slug string) (DeployResult, error)
	CreateSiteFromTemplateIn(ctx context.Context, workspace, name, title, slug, lang string) (DeployResult, error)
}

var (
	errNoWorkspaces = errors.New("this server cannot manage workspaces; use the web interface of dropsite")
	// ErrConfirmMove is returned by move_site without a matching confirm.
	ErrConfirmMove = errors.New("not moved: tell the user who will be able to change the site after the move, ask " +
		"them to confirm, then call move_site again with confirm set to the site's slug or ID exactly as shown by get_site")
	// ErrConfirmMember is returned by remove_workspace_member without a matching confirm.
	ErrConfirmMember = errors.New("not removed: ask the user to confirm that this person loses access to every site " +
		"of the workspace, then call remove_workspace_member again with confirm set to the same e-mail")
)

// sameEmail reports whether confirm repeats email.
func sameEmail(confirm, email string) bool {
	c := strings.TrimSpace(confirm)
	return c != "" && strings.EqualFold(c, strings.TrimSpace(email))
}
