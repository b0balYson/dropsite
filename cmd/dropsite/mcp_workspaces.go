package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Shared workspaces of the local server, through the API.

var _ mcp.WorkspaceBackend = localBackend{}

// workspaceError keeps the template's own not-found answer; any other 404
// is the workspace text, like the remote server.
func workspaceError(err error) error {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Code == domain.CodeTemplateNotFound {
		return toolErrorFor(err, mcp.NotFoundTemplate)
	}
	return toolErrorFor(err, mcp.NotFoundWorkspace)
}

func fromWorkspace(w *client.Workspace) mcp.Workspace {
	members := make([]string, 0, len(w.Members))
	for _, m := range w.Members {
		members = append(members, m.Email)
	}
	return mcp.Workspace{ID: w.ID, Name: w.Name, Members: members, SiteCount: w.SiteCount}
}

// checkWorkspace refuses a workspace argument that is not an ID, like the
// remote server; optional allows "" (personal).
func checkWorkspace(workspace string, optional bool) error {
	if workspace == "" && optional {
		return nil
	}
	if n, err := strconv.ParseInt(workspace, 10, 64); err != nil || n <= 0 {
		return errors.New(mcp.NotFoundWorkspace)
	}
	return nil
}

func (b localBackend) ListWorkspaces(ctx context.Context) ([]mcp.Workspace, error) {
	l, err := b.c.ListWorkspaces(ctx)
	if err != nil {
		return nil, workspaceError(err)
	}
	out := make([]mcp.Workspace, 0, len(l.Items))
	for _, w := range l.Items {
		full, err := b.c.GetWorkspace(ctx, w.ID)
		if err != nil {
			return nil, workspaceError(err)
		}
		out = append(out, fromWorkspace(full))
	}
	return out, nil
}

func (b localBackend) CreateWorkspace(ctx context.Context, name string) (mcp.Workspace, error) {
	w, err := b.c.CreateWorkspace(ctx, name)
	if err != nil {
		return mcp.Workspace{}, workspaceError(err)
	}
	return fromWorkspace(w), nil
}

func (b localBackend) AddWorkspaceMember(ctx context.Context, workspace, email string) (mcp.Workspace, error) {
	if err := checkWorkspace(workspace, false); err != nil {
		return mcp.Workspace{}, err
	}
	w, err := b.c.AddWorkspaceMember(ctx, workspace, email)
	if err != nil {
		return mcp.Workspace{}, workspaceError(err)
	}
	return fromWorkspace(w), nil
}

func (b localBackend) RemoveWorkspaceMember(ctx context.Context, workspace, email string) (mcp.MemberRemoval, error) {
	if err := checkWorkspace(workspace, false); err != nil {
		return mcp.MemberRemoval{}, err
	}
	w, err := b.c.GetWorkspace(ctx, workspace)
	if err != nil {
		return mcp.MemberRemoval{}, workspaceError(err)
	}
	for _, m := range w.Members {
		if strings.EqualFold(m.Email, strings.TrimSpace(email)) {
			deleted, err := b.c.RemoveWorkspaceMember(ctx, workspace, m.UserID)
			if err != nil {
				return mcp.MemberRemoval{}, workspaceError(err)
			}
			return mcp.MemberRemoval{Removed: email, WorkspaceDeleted: deleted}, nil
		}
	}
	return mcp.MemberRemoval{}, errors.New(mcp.NotFoundWorkspace)
}

func (b localBackend) MoveSite(ctx context.Context, site, workspace string) (mcp.Site, error) {
	if err := checkWorkspace(workspace, true); err != nil {
		return mcp.Site{}, err
	}
	s, err := b.c.MoveSite(ctx, site, workspace)
	if err != nil {
		return mcp.Site{}, workspaceError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) CreateSiteIn(ctx context.Context, workspace, title, slug string) (mcp.Site, error) {
	if err := checkWorkspace(workspace, true); err != nil {
		return mcp.Site{}, err
	}
	s, err := b.c.CreateSite(ctx, client.CreateSiteRequest{Title: title, Slug: slug, WorkspaceID: workspace})
	if err != nil {
		return mcp.Site{}, workspaceError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) CloneSiteIn(ctx context.Context, workspace, ref, title, slug string) (mcp.DeployResult, error) {
	if err := checkWorkspace(workspace, true); err != nil {
		return mcp.DeployResult{}, err
	}
	r, err := b.c.CloneSite(ctx, ref, client.NewSiteRequest{Title: title, Slug: slug, WorkspaceID: workspace})
	if err != nil {
		return mcp.DeployResult{}, workspaceError(err)
	}
	return fromResult(r), nil
}

func (b localBackend) CreateSiteFromTemplateIn(ctx context.Context, workspace, name, title, slug, lang string) (mcp.DeployResult, error) {
	if err := checkWorkspace(workspace, true); err != nil {
		return mcp.DeployResult{}, err
	}
	r, err := b.c.CreateSiteFromTemplate(ctx, name, client.NewSiteRequest{Title: title, Slug: slug, Lang: lang, WorkspaceID: workspace})
	if err != nil {
		return mcp.DeployResult{}, workspaceError(err)
	}
	return fromResult(r), nil
}
