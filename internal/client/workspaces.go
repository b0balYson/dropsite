package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Shared workspaces.

// WorkspaceMember is a member of a workspace.
type WorkspaceMember struct {
	UserID  string    `json:"user_id"`
	Email   string    `json:"email"`
	AddedAt time.Time `json:"added_at"`
}

// Workspace is a shared workspace; Members is set for single-workspace
// responses only.
type Workspace struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	MemberCount int               `json:"member_count"`
	SiteCount   int               `json:"site_count"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Members     []WorkspaceMember `json:"members,omitempty"`
}

func workspacePath(id string) string { return "/workspaces/" + url.PathEscape(id) }

// ListWorkspaces lists the workspaces the user belongs to.
func (c *Client) ListWorkspaces(ctx context.Context) (*List[Workspace], error) {
	var out List[Workspace]
	if err := c.doJSON(ctx, http.MethodGet, "/workspaces", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkspace returns a workspace with its members.
func (c *Client) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	var out Workspace
	if err := c.doJSON(ctx, http.MethodGet, workspacePath(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWorkspace creates a workspace with the user as its only member.
func (c *Client) CreateWorkspace(ctx context.Context, name string) (*Workspace, error) {
	var out Workspace
	if err := c.doJSON(ctx, http.MethodPost, "/workspaces", nil, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddWorkspaceMember adds an existing user by e-mail.
func (c *Client) AddWorkspaceMember(ctx context.Context, id, email string) (*Workspace, error) {
	var out Workspace
	if err := c.doJSON(ctx, http.MethodPost, workspacePath(id)+"/members", nil, map[string]string{"email": email}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveWorkspaceMember removes a member (the user's own ID: leave).
func (c *Client) RemoveWorkspaceMember(ctx context.Context, id, userID string) error {
	return c.doJSON(ctx, http.MethodDelete, workspacePath(id)+"/members/"+url.PathEscape(userID), nil, nil, nil)
}

// MoveSite moves a site into a workspace (empty workspaceID: personal).
func (c *Client) MoveSite(ctx context.Context, ref, workspaceID string) (*Site, error) {
	var ws *string
	if workspaceID != "" {
		ws = &workspaceID
	}
	var out Site
	if err := c.doJSON(ctx, http.MethodPost, sitePath(ref)+"/move", nil, map[string]*string{"workspace_id": ws}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
