package service

import (
	"context"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// team creates a workspace of ann with bob as the second member.
func team(t *testing.T, e *env) (ann, bob, eve Actor, ws WorkspaceView) {
	t.Helper()
	ctx := context.Background()
	ann, bob, eve = e.user("ann@pp.test"), e.user("bob@pp.test"), e.user("eve@pp.test")
	ws, err := e.svc.CreateWorkspace(ctx, ann, "  Marketing ")
	if err != nil {
		t.Fatal(err)
	}
	if ws, err = e.svc.AddWorkspaceMember(ctx, ann, ws.ID, "BOB@pp.test"); err != nil {
		t.Fatal(err)
	}
	return ann, bob, eve, ws
}

func TestWorkspaceLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ann, bob, eve, ws := team(t, e)
	if ws.Name != "Marketing" || len(ws.Members) != 2 || ws.MemberCount != 2 {
		t.Fatalf("workspace %+v", ws)
	}

	// Strangers do not see the workspace at all.
	_, err := e.svc.GetWorkspace(ctx, eve, ws.ID)
	wantNotFound(t, err)
	_, err = e.svc.AddWorkspaceMember(ctx, eve, ws.ID, "eve@pp.test")
	wantNotFound(t, err)
	_, err = e.svc.RemoveWorkspaceMember(ctx, eve, ws.ID, bob.User.ID)
	wantNotFound(t, err)
	wantNotFound(t, e.svc.DeleteWorkspace(ctx, eve, ws.ID))
	_, err = e.svc.RenameWorkspace(ctx, eve, ws.ID, "x")
	wantNotFound(t, err)
	if l, _ := e.svc.ListWorkspaces(ctx, eve, false); len(l) != 0 {
		t.Fatalf("eve sees %v", l)
	}
	_, err = e.svc.ListWorkspaces(ctx, eve, true)
	wantNotFound(t, err)

	// Validation and membership errors.
	_, err = e.svc.CreateWorkspace(ctx, ann, " ")
	wantCode(t, err, domain.CodeWorkspaceNameInvalid)
	_, err = e.svc.AddWorkspaceMember(ctx, bob, ws.ID, "nobody@pp.test")
	wantCode(t, err, domain.CodeWorkspaceUserNotFound)
	_, err = e.svc.AddWorkspaceMember(ctx, bob, ws.ID, "ann@pp.test")
	wantCode(t, err, domain.CodeWorkspaceMemberExists)
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, eve.User.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AddWorkspaceMember(ctx, bob, ws.ID, "eve@pp.test")
	wantCode(t, err, domain.CodeWorkspaceUserNotFound)

	// Equal members: bob renames and removes ann.
	if ws, err = e.svc.RenameWorkspace(ctx, bob, ws.ID, "Sales"); err != nil || ws.Name != "Sales" {
		t.Fatalf("rename %+v %v", ws, err)
	}
	site, err := e.svc.CreateSiteIn(ctx, bob, "Shop", "", ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := e.svc.RemoveWorkspaceMember(ctx, bob, ws.ID, ann.User.ID); err != nil || deleted {
		t.Fatalf("remove ann %v %v", deleted, err)
	}
	_, err = e.svc.GetWorkspace(ctx, ann, ws.ID)
	wantNotFound(t, err)

	// The last member cannot leave while sites remain.
	_, err = e.svc.RemoveWorkspaceMember(ctx, bob, ws.ID, bob.User.ID)
	wantCode(t, err, domain.CodeWorkspaceLastMember)
	wantCode(t, e.svc.DeleteWorkspace(ctx, bob, ws.ID), domain.CodeWorkspaceNotEmpty)
	if err := e.svc.DeleteSite(ctx, bob, site.Site.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := e.svc.RemoveWorkspaceMemberByEmail(ctx, bob, ws.ID, "Bob@pp.test")
	if err != nil || !deleted {
		t.Fatalf("leave %v %v", deleted, err)
	}
	_, err = e.svc.GetWorkspace(ctx, e.admin, ws.ID)
	wantNotFound(t, err)

	for _, action := range []string{"workspace.create", "workspace.rename", "workspace.member.add", "workspace.member.remove"} {
		if len(e.auditMeta(action)) == 0 {
			t.Errorf("no audit %s", action)
		}
	}
}

func TestWorkspaceAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, _, eve, ws := team(t, e)
	all, err := e.svc.ListWorkspaces(ctx, e.admin, true)
	if err != nil || len(all) != 1 {
		t.Fatalf("admin list %v %v", all, err)
	}
	if _, err := e.svc.AddWorkspaceMember(ctx, e.admin, ws.ID, "eve@pp.test"); err != nil {
		t.Fatal(err)
	}
	// An administrator acting through the assistant is an ordinary user.
	_, err = e.svc.GetWorkspace(ctx, e.mcpActor(e.admin), ws.ID)
	wantNotFound(t, err)
	_, err = e.svc.ListWorkspaces(ctx, e.mcpActor(e.admin), true)
	wantNotFound(t, err)
	if _, err := e.svc.GetWorkspace(ctx, eve, ws.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMoveSite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ann, bob, eve, ws := team(t, e)
	v, err := e.svc.CreateSite(ctx, ann, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	res := deploy(t, e, ann, v.Site.ID, "<h1>hi</h1>")
	_, err = e.svc.GetSite(ctx, bob, "promo")
	wantNotFound(t, err)

	// Into a workspace the creator is not in: not found.
	other, err := e.svc.CreateWorkspace(ctx, eve, "Other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.MoveSite(ctx, ann, "promo", other.ID)
	wantNotFound(t, err)

	moved, err := e.svc.MoveSite(ctx, ann, "promo", ws.ID)
	if err != nil || moved.Site.WorkspaceID == nil || *moved.Site.WorkspaceID != ws.ID || moved.WorkspaceName != "Marketing" {
		t.Fatalf("move %+v %v", moved, err)
	}
	if moved.Current == nil || moved.Current.ID != res.Deploy.ID || moved.Slug != "promo" {
		t.Fatalf("move changed the site: %+v", moved)
	}
	if body, err := e.served("promo."+testBase, "index.html"); err != nil || body != "<h1>hi</h1>" {
		t.Fatalf("serving after move: %q %v", body, err)
	}
	got, err := e.svc.GetSite(ctx, bob, "promo")
	if err != nil || got.WorkspaceName != "Marketing" {
		t.Fatalf("bob get %+v %v", got, err)
	}
	// Only the creator moves it; repeating the move changes nothing.
	_, err = e.svc.MoveSite(ctx, bob, "promo", 0)
	wantCode(t, err, domain.CodeWorkspaceMoveDenied)
	if _, err := e.svc.MoveSite(ctx, ann, "promo", ws.ID); err != nil {
		t.Fatal(err)
	}
	meta := e.auditMeta("site.move")
	if len(meta) != 1 || meta[0]["to"] != float64(ws.ID) || meta[0]["from"] != "personal" {
		t.Fatalf("audit %v", meta)
	}
	// Site events of a workspace site name the workspace.
	deploy(t, e, bob, "promo", "<h1>bob</h1>")
	dm := e.auditMeta("deploy.create")
	if len(dm) == 0 || dm[0]["workspace_id"] != float64(ws.ID) {
		t.Fatalf("deploy audit %v", dm)
	}

	back, err := e.svc.MoveSite(ctx, ann, "promo", 0)
	if err != nil || back.Site.WorkspaceID != nil {
		t.Fatalf("move back %+v %v", back, err)
	}
	_, err = e.svc.GetSite(ctx, bob, "promo")
	wantNotFound(t, err)

	// An administrator moves any site anywhere.
	if _, err := e.svc.MoveSite(ctx, e.admin, "promo", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSite(ctx, eve, "promo"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceSiteCreation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ann, bob, eve, ws := team(t, e)
	_, err := e.svc.CreateSiteIn(ctx, eve, "x", "", ws.ID)
	wantNotFound(t, err)
	v, err := e.svc.CreateSiteIn(ctx, bob, "Shop", "", ws.ID)
	if err != nil || v.OwnerID != bob.User.ID || v.WorkspaceName != "Marketing" {
		t.Fatalf("create %+v %v", v, err)
	}
	deploy(t, e, ann, v.Site.ID, "<h1>shop</h1>")
	c, err := e.svc.CloneSiteIn(ctx, ann, v.Site.ID, "", "", ws.ID, domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.GetSite(ctx, bob, c.Site.Site.ID); got.Site.WorkspaceID == nil {
		t.Fatal("clone is not in the workspace")
	}
	personal, err := e.svc.CloneSite(ctx, ann, v.Site.ID, "", "", domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetSite(ctx, bob, personal.Site.Site.ID)
	wantNotFound(t, err)
	tpl, err := e.svc.CreateSiteFromTemplateIn(ctx, ann, "cafe", "Cafe", "", "en", ws.ID, domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateSiteFromTemplateIn(ctx, eve, "cafe", "Cafe", "", "en", ws.ID, domain.SourceAPI)
	wantNotFound(t, err)

	// Lists: everything visible, personal only, one workspace.
	all, total, err := e.svc.ListSites(ctx, bob, Page{})
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("bob sites %d %v", total, err)
	}
	if _, n, _ := e.svc.ListSitesIn(ctx, ann, PersonalScope(), Page{}); n != 1 {
		t.Fatalf("ann personal %d", n)
	}
	if _, n, _ := e.svc.ListSitesIn(ctx, ann, WorkspaceScope(ws.ID), Page{}); n != 3 {
		t.Fatalf("ann workspace %d", n)
	}
	_, _, err = e.svc.ListSitesIn(ctx, eve, WorkspaceScope(ws.ID), Page{})
	wantNotFound(t, err)
	_ = tpl

	// Limits count against the creator: ann created two of them plus a personal copy.
	_, _, count, err := e.svc.Me(ctx, ann)
	if err != nil || count != 3 {
		t.Fatalf("ann count %d %v", count, err)
	}
	_, _, count, _ = e.svc.Me(ctx, bob)
	if count != 1 {
		t.Fatalf("bob count %d", count)
	}
}

func TestWorkspaceSiteLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ann, _, _, ws := team(t, e)
	one := int64(1)
	if _, err := e.svc.SetUserLimits(ctx, e.admin, ann.User.ID, domain.LimitOverrides{MaxSitesPerUser: &one}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSite(ctx, ann, "a", ""); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CreateSiteIn(ctx, ann, "b", "", ws.ID)
	wantCode(t, err, domain.CodeSiteLimit)
}
