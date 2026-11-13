package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Shared workspaces (change shared-workspaces): equal members have owner
// rights on the workspace's sites. Every check lives here and in
// resolveSite; a workspace the actor may not see is reported as missing.

// WorkspaceView is a workspace with its members.
type WorkspaceView struct {
	domain.Workspace
	Members []domain.WorkspaceMember
}

func workspaceTarget(id int64) string { return fmt.Sprintf("workspace:%d", id) }

// errWorkspaceUserNotFound is the one answer for every e-mail that does not
// belong to an active user (unknown, blocked or not yet activated): the
// caller must not learn which addresses have accounts. The reason goes to
// the audit log only.
var errWorkspaceUserNotFound = domain.E(domain.KindInvalid, domain.CodeWorkspaceUserNotFound,
	"this person could not be added; check the e-mail, and if it is right, ask an administrator to create the account")

// resolveWorkspace loads a workspace the actor belongs to (any workspace
// for an administrator acting directly).
func (s *Service) resolveWorkspace(ctx context.Context, a Actor, id int64) (domain.Workspace, error) {
	if id <= 0 {
		return domain.Workspace{}, domain.ErrNotFound
	}
	if !a.IsAdmin() {
		ok, err := s.repo.IsWorkspaceMember(ctx, id, a.User.ID)
		if err != nil {
			return domain.Workspace{}, err
		}
		if !ok {
			return domain.Workspace{}, domain.ErrNotFound
		}
	}
	return s.repo.GetWorkspace(ctx, id)
}

// canUseSite reports whether userID has owner rights on site: the owner of
// a personal site, or a member of the site's workspace.
func (s *Service) canUseSite(ctx context.Context, userID int64, site domain.Site) (bool, error) {
	if site.WorkspaceID == nil {
		return site.OwnerID == userID, nil
	}
	return s.repo.IsWorkspaceMember(ctx, *site.WorkspaceID, userID)
}

func (s *Service) workspaceView(ctx context.Context, w domain.Workspace) (WorkspaceView, error) {
	members, err := s.repo.ListWorkspaceMembers(ctx, w.ID)
	if err != nil {
		return WorkspaceView{}, err
	}
	return WorkspaceView{Workspace: w, Members: members}, nil
}

// ListWorkspaces returns the actor's workspaces; all (administrators acting
// directly only) returns every workspace.
func (s *Service) ListWorkspaces(ctx context.Context, a Actor, all bool) ([]domain.Workspace, error) {
	if all {
		if err := requireAdmin(a); err != nil {
			return nil, err
		}
		return s.repo.ListWorkspaces(ctx, nil)
	}
	id := a.User.ID
	return s.repo.ListWorkspaces(ctx, &id)
}

// GetWorkspace returns a visible workspace with its members.
func (s *Service) GetWorkspace(ctx context.Context, a Actor, id int64) (WorkspaceView, error) {
	w, err := s.resolveWorkspace(ctx, a, id)
	if err != nil {
		return WorkspaceView{}, err
	}
	return s.workspaceView(ctx, w)
}

// CreateWorkspace creates a workspace with the actor as its only member.
func (s *Service) CreateWorkspace(ctx context.Context, a Actor, name string) (WorkspaceView, error) {
	name, err := domain.ValidateWorkspaceName(name)
	if err != nil {
		return WorkspaceView{}, err
	}
	id, err := s.repo.CreateWorkspace(ctx, name, a.User.ID, domain.MaxWorkspacesPerUser, s.now())
	if err != nil {
		return WorkspaceView{}, err
	}
	s.audit(ctx, a, "workspace.create", workspaceTarget(id), map[string]any{"name": name})
	return s.GetWorkspace(ctx, a, id)
}

// RenameWorkspace renames a visible workspace.
func (s *Service) RenameWorkspace(ctx context.Context, a Actor, id int64, name string) (WorkspaceView, error) {
	w, err := s.resolveWorkspace(ctx, a, id)
	if err != nil {
		return WorkspaceView{}, err
	}
	name, err = domain.ValidateWorkspaceName(name)
	if err != nil {
		return WorkspaceView{}, err
	}
	if name != w.Name {
		if err := s.repo.RenameWorkspace(ctx, id, name, s.now()); err != nil {
			return WorkspaceView{}, err
		}
		s.audit(ctx, a, "workspace.rename", workspaceTarget(id), map[string]any{"from": w.Name, "name": name})
	}
	return s.GetWorkspace(ctx, a, id)
}

// DeleteWorkspace deletes a visible workspace without sites.
func (s *Service) DeleteWorkspace(ctx context.Context, a Actor, id int64) error {
	w, err := s.resolveWorkspace(ctx, a, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteWorkspace(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, a, "workspace.delete", workspaceTarget(id), map[string]any{"name": w.Name})
	return nil
}

// AddWorkspaceMember adds an existing active user by e-mail. Attempts are
// limited per user, and every e-mail without an active user gets the same
// answer.
func (s *Service) AddWorkspaceMember(ctx context.Context, a Actor, id int64, email string) (WorkspaceView, error) {
	if _, err := s.resolveWorkspace(ctx, a, id); err != nil {
		return WorkspaceView{}, err
	}
	if ok, retry := s.memberLimit.Allow(userKey(a.User.ID)); !ok {
		return WorkspaceView{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": WorkspaceMemberAddsPerHour})
	}
	norm, err := domain.NormalizeEmail(strings.TrimSpace(email))
	if err != nil {
		return WorkspaceView{}, err
	}
	u, err := s.repo.GetUserByEmail(ctx, norm)
	if errors.Is(err, domain.ErrNotFound) {
		return WorkspaceView{}, s.refuseMember(ctx, a, id, norm, "no_account")
	}
	if err != nil {
		return WorkspaceView{}, err
	}
	if u.Status != domain.UserActive {
		return WorkspaceView{}, s.refuseMember(ctx, a, id, norm, "status_"+string(u.Status))
	}
	if err := s.repo.AddWorkspaceMember(ctx, id, u.ID, a.User.ID, domain.MaxWorkspaceMembers,
		domain.MaxWorkspacesPerUser, s.now()); err != nil {
		return WorkspaceView{}, err
	}
	s.audit(ctx, a, "workspace.member.add", workspaceTarget(id), map[string]any{"user": u.ID, "email": u.Email})
	return s.GetWorkspace(ctx, a, id)
}

// refuseMember records why an e-mail could not be added and returns the
// neutral answer.
func (s *Service) refuseMember(ctx context.Context, a Actor, id int64, email, reason string) error {
	s.audit(ctx, a, "workspace.member.refused", workspaceTarget(id), map[string]any{"email": email, "reason": reason})
	return errWorkspaceUserNotFound
}

// RemoveWorkspaceMember removes a member, or the actor itself (leaving).
// The last member of an empty workspace deletes it (deleted true).
func (s *Service) RemoveWorkspaceMember(ctx context.Context, a Actor, id, userID int64) (bool, error) {
	if _, err := s.resolveWorkspace(ctx, a, id); err != nil {
		return false, err
	}
	members, err := s.repo.ListWorkspaceMembers(ctx, id)
	if err != nil {
		return false, err
	}
	email := ""
	for _, m := range members {
		if m.UserID == userID {
			email = m.Email
		}
	}
	if email == "" {
		return false, domain.ErrNotFound
	}
	deleted, err := s.repo.RemoveWorkspaceMember(ctx, id, userID)
	if err != nil {
		return false, err
	}
	s.audit(ctx, a, "workspace.member.remove", workspaceTarget(id), map[string]any{
		"user": userID, "email": email, "left": userID == a.User.ID, "workspace_deleted": deleted,
	})
	return deleted, nil
}

// RemoveWorkspaceMemberByEmail is RemoveWorkspaceMember for a member named
// by e-mail (the assistant knows members by e-mail).
func (s *Service) RemoveWorkspaceMemberByEmail(ctx context.Context, a Actor, id int64, email string) (bool, error) {
	if _, err := s.resolveWorkspace(ctx, a, id); err != nil {
		return false, err
	}
	members, err := s.repo.ListWorkspaceMembers(ctx, id)
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if strings.EqualFold(m.Email, strings.TrimSpace(email)) {
			return s.RemoveWorkspaceMember(ctx, a, id, m.UserID)
		}
	}
	return false, domain.E(domain.KindNotFound, domain.CodeNotFound, "this e-mail is not a member of the workspace")
}

// withSiteWorkspace adds the workspace of a site target to audit metadata,
// so every site event of a workspace site names the workspace.
func (s *Service) withSiteWorkspace(ctx context.Context, target string, meta map[string]any) map[string]any {
	siteID, ok := strings.CutPrefix(target, "site:")
	if !ok || siteID == "" {
		return meta
	}
	if _, set := meta["workspace_id"]; set {
		return meta
	}
	ws, err := s.repo.SiteWorkspaceID(context.WithoutCancel(ctx), siteID)
	if err != nil {
		s.log.Error("audit: site workspace", "site", siteID, "err", err)
		return meta
	}
	if ws == nil {
		return meta
	}
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	out["workspace_id"] = *ws
	return out
}
