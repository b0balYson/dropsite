package remote

import (
	"context"
	"errors"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.WorkspaceBackend = backend{}

// workspaceOf returns the workspace fields of a site ("" for personal).
func workspaceOf(id *int64, name string) (string, string) {
	if id == nil {
		return "", ""
	}
	return strconv.FormatInt(*id, 10), name
}

// workspaceID parses a workspace argument; "" means personal (0).
func workspaceID(ref string) (int64, error) {
	if ref == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New(mcp.NotFoundWorkspace)
	}
	return id, nil
}

func (b backend) workspaceError(err error) error {
	if err != nil && domain.AsError(err).Kind == domain.KindNotFound {
		return b.errorFor(err, mcp.NotFoundWorkspace)
	}
	return b.toolError(err)
}

func toWorkspace(v service.WorkspaceView) mcp.Workspace {
	members := make([]string, 0, len(v.Members))
	for _, m := range v.Members {
		members = append(members, m.Email)
	}
	return mcp.Workspace{ID: strconv.FormatInt(v.ID, 10), Name: v.Name, Members: members, SiteCount: v.SiteCount}
}

func (b backend) ListWorkspaces(ctx context.Context) ([]mcp.Workspace, error) {
	rows, err := b.svc.ListWorkspaces(ctx, b.actor, false)
	if err != nil {
		return nil, b.workspaceError(err)
	}
	out := make([]mcp.Workspace, 0, len(rows))
	for _, w := range rows {
		v, err := b.svc.GetWorkspace(ctx, b.actor, w.ID)
		if err != nil {
			return nil, b.workspaceError(err)
		}
		out = append(out, toWorkspace(v))
	}
	return out, nil
}

func (b backend) CreateWorkspace(ctx context.Context, name string) (mcp.Workspace, error) {
	v, err := b.svc.CreateWorkspace(ctx, b.actor, name)
	if err != nil {
		return mcp.Workspace{}, b.workspaceError(err)
	}
	return toWorkspace(v), nil
}

func (b backend) AddWorkspaceMember(ctx context.Context, workspace, email string) (mcp.Workspace, error) {
	id, err := workspaceID(workspace)
	if err != nil || id == 0 {
		return mcp.Workspace{}, errors.New(mcp.NotFoundWorkspace)
	}
	v, err := b.svc.AddWorkspaceMember(ctx, b.actor, id, email)
	if err != nil {
		return mcp.Workspace{}, b.workspaceError(err)
	}
	return toWorkspace(v), nil
}

func (b backend) RemoveWorkspaceMember(ctx context.Context, workspace, email string) (mcp.MemberRemoval, error) {
	id, err := workspaceID(workspace)
	if err != nil || id == 0 {
		return mcp.MemberRemoval{}, errors.New(mcp.NotFoundWorkspace)
	}
	deleted, err := b.svc.RemoveWorkspaceMemberByEmail(ctx, b.actor, id, email)
	if err != nil {
		return mcp.MemberRemoval{}, b.workspaceError(err)
	}
	return mcp.MemberRemoval{Removed: email, WorkspaceDeleted: deleted}, nil
}

func (b backend) MoveSite(ctx context.Context, site, workspace string) (mcp.Site, error) {
	id, err := workspaceID(workspace)
	if err != nil {
		return mcp.Site{}, err
	}
	v, err := b.svc.MoveSite(ctx, b.actor, site, id)
	if err != nil {
		return mcp.Site{}, b.workspaceError(err)
	}
	return toSite(v), nil
}

func (b backend) CreateSiteIn(ctx context.Context, workspace, title, slug string) (mcp.Site, error) {
	id, err := workspaceID(workspace)
	if err != nil {
		return mcp.Site{}, err
	}
	v, err := b.svc.CreateSiteIn(ctx, b.actor, title, slug, id)
	if err != nil {
		return mcp.Site{}, b.workspaceError(err)
	}
	return toSite(v), nil
}

func (b backend) CloneSiteIn(ctx context.Context, workspace, ref, title, slug string) (mcp.DeployResult, error) {
	id, err := workspaceID(workspace)
	if err != nil {
		return mcp.DeployResult{}, err
	}
	r, err := b.svc.CloneSiteIn(ctx, b.actor, ref, title, slug, id, domain.SourceMCP)
	if err != nil {
		return mcp.DeployResult{}, b.workspaceError(err)
	}
	return toResult(r), nil
}

func (b backend) CreateSiteFromTemplateIn(ctx context.Context, workspace, name, title, slug, lang string) (mcp.DeployResult, error) {
	id, err := workspaceID(workspace)
	if err != nil {
		return mcp.DeployResult{}, err
	}
	r, err := b.svc.CreateSiteFromTemplateIn(ctx, b.actor, name, title, slug, lang, id, domain.SourceMCP)
	if err != nil {
		return mcp.DeployResult{}, b.workspaceError(err)
	}
	return toResult(r), nil
}
