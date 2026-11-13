package store

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestWorkspaceMembership(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	a := mkUser(t, db, "a@example.test", domain.UserActive)
	b := mkUser(t, db, "b@example.test", domain.UserActive)

	ws, err := db.CreateWorkspace(ctx, "Team", a, 2, t0)
	must(t, err)
	_, err = db.CreateWorkspace(ctx, "Second", a, 2, t0)
	must(t, err)
	_, err = db.CreateWorkspace(ctx, "Third", a, 2, t0)
	wantCode(t, err, domain.CodeWorkspaceLimit)

	must(t, db.AddWorkspaceMember(ctx, ws, b, a, 10, 10, t0))
	wantCode(t, db.AddWorkspaceMember(ctx, ws, b, a, 10, 10, t0), domain.CodeWorkspaceMemberExists)
	c := mkUser(t, db, "c@example.test", domain.UserActive)
	wantCode(t, db.AddWorkspaceMember(ctx, ws, c, a, 2, 10, t0), domain.CodeWorkspaceLimit)

	ok, err := db.IsWorkspaceMember(ctx, ws, b)
	if err != nil || !ok {
		t.Fatalf("member %v %v", ok, err)
	}
	members, err := db.ListWorkspaceMembers(ctx, ws)
	if err != nil || len(members) != 2 || members[0].Email != "a@example.test" {
		t.Fatalf("members %+v %v", members, err)
	}
	mine, err := db.ListWorkspaces(ctx, &b)
	if err != nil || len(mine) != 1 || mine[0].Name != "Team" || mine[0].MemberCount != 2 {
		t.Fatalf("list %+v %v", mine, err)
	}
	all, err := db.ListWorkspaces(ctx, nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("all %+v %v", all, err)
	}
	must(t, db.RenameWorkspace(ctx, ws, "Marketing", t0.Add(time.Minute)))
	got, err := db.GetWorkspace(ctx, ws)
	if err != nil || got.Name != "Marketing" || got.CreatedBy == nil || *got.CreatedBy != a {
		t.Fatalf("get %+v %v", got, err)
	}

	// A site in the workspace keeps the last member in.
	mkSite(t, db, "s1", a, "", t0)
	must(t, db.MoveSite(ctx, "s1", &ws, &a, t0))
	deleted, err := db.RemoveWorkspaceMember(ctx, ws, b)
	if err != nil || deleted {
		t.Fatalf("remove b %v %v", deleted, err)
	}
	wantNotFound(t, func() error { _, err := db.RemoveWorkspaceMember(ctx, ws, b); return err }())
	_, err = db.RemoveWorkspaceMember(ctx, ws, a)
	wantCode(t, err, domain.CodeWorkspaceLastMember)
	wantCode(t, db.DeleteWorkspace(ctx, ws), domain.CodeWorkspaceNotEmpty)

	// Once the site is deleted, the last member takes the workspace along.
	if _, err := db.SoftDeleteSite(ctx, "s1", t0); err != nil {
		t.Fatal(err)
	}
	deleted, err = db.RemoveWorkspaceMember(ctx, ws, a)
	if err != nil || !deleted {
		t.Fatalf("remove last %v %v", deleted, err)
	}
	wantNotFound(t, func() error { _, err := db.GetWorkspace(ctx, ws); return err }())
	if id, err := db.SiteWorkspaceID(ctx, "s1"); err != nil || id != nil {
		t.Fatalf("deleted site still in workspace: %v %v", id, err)
	}
}

func TestWorkspaceSitesVisibility(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	a := mkUser(t, db, "a@example.test", domain.UserActive)
	b := mkUser(t, db, "b@example.test", domain.UserActive)
	c := mkUser(t, db, "c@example.test", domain.UserActive)
	ws, err := db.CreateWorkspace(ctx, "Team", a, 10, t0)
	must(t, err)
	must(t, db.AddWorkspaceMember(ctx, ws, b, a, 10, 10, t0))
	mkSite(t, db, "pa", a, "", t0)
	mkSite(t, db, "pb", b, "", t0)
	mkSite(t, db, "wa", a, "", t0)
	mkSite(t, db, "pc", c, "", t0)

	// Moving into a workspace the user is not in is refused.
	wantNotFound(t, db.MoveSite(ctx, "pc", &ws, &c, t0))
	must(t, db.MoveSite(ctx, "wa", &ws, &a, t0))
	site, err := db.GetSite(ctx, "wa")
	if err != nil || site.WorkspaceID == nil || *site.WorkspaceID != ws {
		t.Fatalf("moved site %+v %v", site, err)
	}

	ids := func(f SiteFilter) map[string]string {
		t.Helper()
		rows, total, err := db.ListSites(ctx, f)
		if err != nil || total != len(rows) {
			t.Fatalf("list %v %d", err, total)
		}
		out := map[string]string{}
		for _, r := range rows {
			out[r.ID] = r.WorkspaceName
		}
		return out
	}
	if got := ids(SiteFilter{VisibleTo: &b}); len(got) != 2 || got["wa"] != "Team" || got["pb"] != "" {
		t.Fatalf("visible to b: %v", got)
	}
	if got := ids(SiteFilter{VisibleTo: &c}); len(got) != 1 || got["pc"] != "" {
		t.Fatalf("visible to c: %v", got)
	}
	personal := int64(0)
	if got := ids(SiteFilter{VisibleTo: &a, Workspace: &personal}); len(got) != 1 || got["pa"] != "" {
		t.Fatalf("personal of a: %v", got)
	}
	if got := ids(SiteFilter{VisibleTo: &b, Workspace: &ws}); len(got) != 1 || got["wa"] != "Team" {
		t.Fatalf("workspace for b: %v", got)
	}
	if got := ids(SiteFilter{}); len(got) != 4 {
		t.Fatalf("all: %v", got)
	}

	// Schedules of every visible site.
	mkDeploy(t, db, "wa", "d1", a, 1, t0)
	_, err = db.CreateSchedule(ctx, domain.ScheduledPublish{SiteID: "wa", DeployID: "d1", PublishAt: t0.Add(time.Hour), CreatedAt: t0}, 5)
	must(t, err)
	if rows, err := db.ListSchedules(ctx, b, "", t0, 10); err != nil || len(rows) != 1 {
		t.Fatalf("schedules of b: %v %v", rows, err)
	}
	if rows, err := db.ListSchedules(ctx, c, "", t0, 10); err != nil || len(rows) != 0 {
		t.Fatalf("schedules of c: %v %v", rows, err)
	}

	must(t, db.MoveSite(ctx, "wa", nil, nil, t0))
	if got := ids(SiteFilter{VisibleTo: &b}); len(got) != 1 {
		t.Fatalf("after move out: %v", got)
	}
}
