package main

import (
	"context"
	"strconv"
	"testing"
)

// cliAs is a CLI environment logged in to the real API of the rig.
func (r *parityRig) cliAs(token string) *testEnv {
	r.t.Helper()
	e := newTestEnv(r.t)
	e.env[envServer] = r.api.URL
	e.env[envToken] = token
	return e
}

func TestCLIWorkspaces(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann, bob := r.env.User("ann@pp.test"), r.env.User("bob@pp.test")
	eve := r.env.User("eve@pp.test")
	ws, err := r.env.Svc.CreateWorkspace(ctx, ann, "Marketing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.env.Svc.AddWorkspaceMember(ctx, ann, ws.ID, "bob@pp.test"); err != nil {
		t.Fatal(err)
	}
	wsID := strconv.FormatInt(ws.ID, 10)
	annCLI := r.cliAs(r.env.Token(ann, "cli"))
	bobCLI := r.cliAs(r.env.Token(bob, "cli"))
	eveCLI := r.cliAs(r.env.Token(eve, "cli"))

	res := annCLI.run("workspaces")
	expectCode(t, res, 0)
	expectContains(t, "stdout", res.stdout, "Marketing", "MEMBERS")

	// A new site goes into the workspace and the folder remembers it.
	dir := siteDir(t, nil)
	res = annCLI.run("deploy", dir, "--workspace", wsID)
	expectCode(t, res, 0)
	proj, ok, err := readProject(dir)
	if err != nil || !ok {
		t.Fatalf("project %v %v", ok, err)
	}
	v, err := r.env.Svc.GetSite(ctx, bob, proj.SiteID)
	if err != nil || v.Site.WorkspaceID == nil {
		t.Fatalf("site not in workspace: %+v %v", v.Site, err)
	}

	// A colleague publishes the same linked folder; a stranger cannot.
	expectCode(t, bobCLI.run("deploy", dir), 0)
	res = eveCLI.run("deploy", dir)
	expectCode(t, res, 1)

	res = bobCLI.run("sites")
	expectCode(t, res, 0)
	expectContains(t, "stdout", res.stdout, "WORKSPACE", "Marketing")

	res = bobCLI.run("clone", proj.SiteID, "--workspace", wsID, "--json")
	expectCode(t, res, 0)
	expectCode(t, eveCLI.run("deploy", siteDir(t, nil), "--workspace", wsID), 1)
}
