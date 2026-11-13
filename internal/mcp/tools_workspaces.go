package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type createWorkspaceArgs struct {
	Name string `json:"name" jsonschema:"name of the workspace, e.g. the team or client name"`
}

type workspaceMemberArgs struct {
	Workspace string `json:"workspace" jsonschema:"workspace_id from list_workspaces"`
	Email     string `json:"email" jsonschema:"e-mail of a person who already has an account on this dropsite server"`
}

type removeMemberArgs struct {
	Workspace string `json:"workspace" jsonschema:"workspace_id from list_workspaces"`
	Email     string `json:"email" jsonschema:"e-mail of the member to remove; the user's own e-mail means leaving the workspace"`
	Confirm   string `json:"confirm" jsonschema:"the same e-mail, typed again after the user explicitly confirmed"`
}

type moveSiteArgs struct {
	Site      string `json:"site" jsonschema:"site ID or slug"`
	Workspace string `json:"workspace,omitempty" jsonschema:"workspace_id from list_workspaces; empty moves the site back to the user's personal sites"`
	Confirm   string `json:"confirm" jsonschema:"the site's slug or ID, typed again after the user explicitly confirmed the move"`
}

type workspacesOut struct {
	Workspaces []Workspace `json:"workspaces"`
}

// registerWorkspaces adds the shared workspace tools (with the cabinet
// tools); the backend must implement WorkspaceBackend.
func (t tools) registerWorkspaces(s *sdk.Server, o Options) {
	if !o.Cabinet {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "list_workspaces", Annotations: readOnly("List workspaces"),
		Description: "List the shared workspaces the user belongs to, with members and number of sites (\"let Anna " +
			"work on our sites\", \"who has access?\"). Every " +
			"member of a workspace can do everything with its sites that an owner can: publish, change files, " +
			"forms, data, domains, delete. There are no roles. Rules: get_guide topic workspaces."}, t.listWorkspaces)
	sdk.AddTool(s, &sdk.Tool{Name: "create_workspace", Annotations: writes("Create workspace", false, false),
		Description: "Create a shared workspace for a team, with the user as its only member, when list_workspaces " +
			"has none for this team. Then add colleagues with add_workspace_member and create or move sites into it."}, t.createWorkspace)
	sdk.AddTool(s, &sdk.Tool{Name: "add_workspace_member", Annotations: writes("Add workspace member", false, false),
		Description: "Add a colleague to a workspace by e-mail. The person must already have an account on this " +
			"dropsite server: new accounts are created only by the administrator, never here. Tell the user the " +
			"colleague can now change every site of the workspace."}, t.addWorkspaceMember)
	sdk.AddTool(s, &sdk.Tool{Name: "remove_workspace_member", Annotations: writes("Remove workspace member", true, true),
		Description: "Remove a member from a workspace, or leave it (the user's own e-mail). The person loses access " +
			"to every site of the workspace, including sites they created. The last member cannot leave while the " +
			"workspace has sites; an empty workspace is deleted when its last member leaves. Only after the user " +
			"explicitly confirmed: pass the same e-mail in confirm."}, t.removeWorkspaceMember)
	sdk.AddTool(s, &sdk.Tool{Name: "move_site", Annotations: writes("Move site", true, true),
		Description: "Move a site the user created into one of their workspaces, so every member can change it, or " +
			"back to the user's personal sites (empty workspace). Links, versions, forms, data and settings stay " +
			"the same. Only the person who created the site can move it. This changes who can edit the site: say " +
			"who will have access, wait for an explicit yes, then pass the site's slug or ID in confirm."}, t.moveSite)
}

func (t tools) workspaces(ctx context.Context, req *sdk.CallToolRequest) (WorkspaceBackend, Backend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	wb, ok := b.(WorkspaceBackend)
	if !ok {
		return nil, nil, errNoWorkspaces
	}
	return wb, b, nil
}

func (t tools) listWorkspaces(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, workspacesOut, error) {
	wb, _, err := t.workspaces(ctx, req)
	if err != nil {
		return nil, workspacesOut{}, err
	}
	ws, err := wb.ListWorkspaces(ctx)
	if ws == nil {
		ws = []Workspace{}
	}
	return nil, workspacesOut{Workspaces: ws}, err
}

func (t tools) createWorkspace(ctx context.Context, req *sdk.CallToolRequest, in createWorkspaceArgs) (*sdk.CallToolResult, Workspace, error) {
	wb, _, err := t.workspaces(ctx, req)
	if err != nil {
		return nil, Workspace{}, err
	}
	w, err := wb.CreateWorkspace(ctx, in.Name)
	return nil, w, err
}

func (t tools) addWorkspaceMember(ctx context.Context, req *sdk.CallToolRequest, in workspaceMemberArgs) (*sdk.CallToolResult, Workspace, error) {
	wb, _, err := t.workspaces(ctx, req)
	if err != nil {
		return nil, Workspace{}, err
	}
	w, err := wb.AddWorkspaceMember(ctx, in.Workspace, in.Email)
	return nil, w, err
}

func (t tools) removeWorkspaceMember(ctx context.Context, req *sdk.CallToolRequest, in removeMemberArgs) (*sdk.CallToolResult, MemberRemoval, error) {
	wb, _, err := t.workspaces(ctx, req)
	if err != nil {
		return nil, MemberRemoval{}, err
	}
	if !sameEmail(in.Confirm, in.Email) {
		return nil, MemberRemoval{}, ErrConfirmMember
	}
	r, err := wb.RemoveWorkspaceMember(ctx, in.Workspace, in.Email)
	return nil, r, err
}

func (t tools) moveSite(ctx context.Context, req *sdk.CallToolRequest, in moveSiteArgs) (*sdk.CallToolResult, Site, error) {
	wb, b, err := t.workspaces(ctx, req)
	if err != nil {
		return nil, Site{}, err
	}
	s, err := b.GetSite(ctx, in.Site)
	if err != nil {
		return nil, Site{}, err
	}
	if !sameRef(in.Confirm, s) {
		return nil, Site{}, ErrConfirmMove
	}
	moved, err := wb.MoveSite(ctx, s.ID, in.Workspace)
	return nil, moved, err
}

// inWorkspace returns the workspace backend for a creation tool called with
// a workspace.
func (t tools) inWorkspace(ctx context.Context, req *sdk.CallToolRequest) (WorkspaceBackend, error) {
	wb, _, err := t.workspaces(ctx, req)
	return wb, err
}
