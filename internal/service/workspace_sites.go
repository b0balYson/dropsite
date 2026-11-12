package service

import (
	"context"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// SiteScope narrows a site list: nil Workspace means every visible site,
// 0 personal sites only, otherwise one workspace.
type SiteScope struct {
	Workspace *int64
}

// PersonalScope lists only personal sites.
func PersonalScope() SiteScope {
	zero := int64(0)
	return SiteScope{Workspace: &zero}
}

// WorkspaceScope lists only the sites of one workspace.
func WorkspaceScope(id int64) SiteScope { return SiteScope{Workspace: &id} }

// ListSitesIn returns the sites visible to the actor (personal sites and
// sites of the actor's workspaces), narrowed by scope. A workspace the
// actor does not belong to is not found.
func (s *Service) ListSitesIn(ctx context.Context, a Actor, scope SiteScope, p store.Page) ([]SiteSummaryView, int, error) {
	if scope.Workspace != nil && *scope.Workspace != 0 {
		ok, err := s.repo.IsWorkspaceMember(ctx, *scope.Workspace, a.User.ID)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			return nil, 0, domain.ErrNotFound
		}
	}
	user := a.User.ID
	rows, total, err := s.repo.ListSites(ctx, store.SiteFilter{VisibleTo: &user, Workspace: scope.Workspace, Page: p})
	if err != nil {
		return nil, 0, err
	}
	return s.summaries(rows), total, nil
}

// targetWorkspaceKey carries the workspace a new site is created in from
// CreateSiteIn, CloneSiteIn and CreateSiteFromTemplateIn down to
// CreateSite, so the shared creation paths keep their signatures.
type targetWorkspaceKey struct{}

func withTargetWorkspace(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, targetWorkspaceKey{}, id)
}

// targetWorkspace returns the workspace a site is being created in (nil for
// a personal site), checking that the actor may use it.
func (s *Service) targetWorkspace(ctx context.Context, a Actor) (*int64, error) {
	id, ok := ctx.Value(targetWorkspaceKey{}).(int64)
	if !ok || id == 0 {
		return nil, nil
	}
	if _, err := s.resolveWorkspace(ctx, a, id); err != nil {
		return nil, err
	}
	return &id, nil
}

// inWorkspace checks the target workspace up front (0: personal) and
// returns the context the creation runs with.
func (s *Service) inWorkspace(ctx context.Context, a Actor, workspaceID int64) (context.Context, error) {
	if workspaceID == 0 {
		return ctx, nil
	}
	if _, err := s.resolveWorkspace(ctx, a, workspaceID); err != nil {
		return nil, err
	}
	return withTargetWorkspace(ctx, workspaceID), nil
}

// CreateSiteIn is CreateSite in a workspace of the actor (0: personal). The
// actor stays the site's creator; limits count against the creator.
func (s *Service) CreateSiteIn(ctx context.Context, a Actor, title, slug string, workspaceID int64) (SiteView, error) {
	ctx, err := s.inWorkspace(ctx, a, workspaceID)
	if err != nil {
		return SiteView{}, err
	}
	return s.CreateSite(ctx, a, title, slug)
}

// CloneSiteIn is CloneSite with the copy created in a workspace (0: personal).
func (s *Service) CloneSiteIn(ctx context.Context, a Actor, ref, title, slug string, workspaceID int64,
	source domain.DeploySource) (DeployResult, error) {
	ctx, err := s.inWorkspace(ctx, a, workspaceID)
	if err != nil {
		return DeployResult{}, err
	}
	return s.CloneSite(ctx, a, ref, title, slug, source)
}

// CreateSiteFromTemplateIn is CreateSiteFromTemplate in a workspace (0: personal).
func (s *Service) CreateSiteFromTemplateIn(ctx context.Context, a Actor, name, title, slug, lang string,
	workspaceID int64, source domain.DeploySource) (DeployResult, error) {
	ctx, err := s.inWorkspace(ctx, a, workspaceID)
	if err != nil {
		return DeployResult{}, err
	}
	return s.CreateSiteFromTemplate(ctx, a, name, title, slug, lang, source)
}

// MoveSite puts a site into a workspace (workspaceID 0: back to the personal
// sites of its creator). Only the creator, who must belong to the target
// workspace, or an administrator acting directly may move a site.
// Addresses, releases, data and settings stay as they are.
func (s *Service) MoveSite(ctx context.Context, a Actor, ref string, workspaceID int64) (SiteView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	if !a.IsAdmin() && site.OwnerID != a.User.ID {
		return SiteView{}, domain.ErrWorkspaceMoveDenied
	}
	var target, member *int64
	if workspaceID != 0 {
		if _, err := s.resolveWorkspace(ctx, a, workspaceID); err != nil {
			return SiteView{}, err
		}
		target = &workspaceID
		if !a.IsAdmin() {
			id := a.User.ID
			member = &id
		}
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return SiteView{}, err
	}
	if sameWorkspace(site.WorkspaceID, target) {
		return s.view(ctx, site)
	}
	if err := s.repo.MoveSite(ctx, site.ID, target, member, s.now()); err != nil {
		return SiteView{}, err
	}
	meta := map[string]any{"from": workspaceMeta(site.WorkspaceID), "to": workspaceMeta(target)}
	if target != nil {
		meta["workspace_id"] = *target
	}
	s.audit(ctx, a, "site.move", siteTarget(site.ID), meta)
	site.WorkspaceID = target
	return s.view(ctx, site)
}

func sameWorkspace(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// workspaceMeta names a site's place in the audit log: a workspace ID or
// "personal".
func workspaceMeta(id *int64) any {
	if id == nil {
		return "personal"
	}
	return *id
}
