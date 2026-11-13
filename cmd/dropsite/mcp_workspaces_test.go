package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// Both MCP servers give one answer for unknown and blocked addresses and
// pass on the per-user limit on member additions.
func TestStdioWorkspaceMemberProbing(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	blocked := r.env.User("blocked@pp.test")
	if _, err := r.env.Svc.SetUserBlocked(ctx, r.env.Admin, blocked.User.ID, true); err != nil {
		t.Fatal(err)
	}
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	ws := mustTool(t, local, "create_workspace", map[string]any{"name": "Team"})
	id, _ := ws["workspace_id"].(string)

	_, unknown := outputs(t, local, "add_workspace_member", map[string]any{"workspace": id, "email": "nobody@pp.test"})
	_, blockedMsg := outputs(t, web, "add_workspace_member", map[string]any{"workspace": id, "email": "blocked@pp.test"})
	if unknown == "" || unknown != blockedMsg || !strings.Contains(unknown, domain.CodeWorkspaceUserNotFound) {
		t.Fatalf("answers differ or wrong:\nunknown %q\nblocked %q", unknown, blockedMsg)
	}
	// Both servers act as ann and share her budget of attempts.
	for i := 2; i < service.WorkspaceMemberAddsPerHour; i++ {
		outputs(t, local, "add_workspace_member", map[string]any{"workspace": id, "email": fmt.Sprintf("guess%d@pp.test", i)})
	}
	same(t, local, web, "add_workspace_member", map[string]any{"workspace": id, "email": "guess@pp.test"})
	wantToolErr(t, web, "add_workspace_member", map[string]any{"workspace": id, "email": "guess@pp.test"}, "too many requests")
}

func TestStdioWorkspaceTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	r.env.User("bob@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)

	ws := mustTool(t, local, "create_workspace", map[string]any{"name": "Marketing"})
	id, _ := ws["workspace_id"].(string)
	if id == "" || ws["name"] != "Marketing" {
		t.Fatalf("create: %v", ws)
	}
	if out := mustTool(t, web, "add_workspace_member", map[string]any{"workspace": id, "email": "bob@pp.test"}); len(out["members"].([]any)) != 2 {
		t.Fatalf("add: %v", out)
	}
	same(t, local, web, "add_workspace_member", map[string]any{"workspace": id, "email": "nobody@pp.test"})
	same(t, local, web, "add_workspace_member", map[string]any{"workspace": "999", "email": "bob@pp.test"})
	same(t, local, web, "add_workspace_member", map[string]any{"workspace": "x", "email": "bob@pp.test"})
	l := same(t, local, web, "list_workspaces", map[string]any{})
	if len(l["workspaces"].([]any)) != 1 {
		t.Fatalf("list: %v", l)
	}

	// Sites created in the workspace, and moved in with confirmation.
	s := mustTool(t, local, "create_site", map[string]any{"title": "Shop", "workspace": id})
	if s["workspace_id"] != id || s["workspace"] != "Marketing" {
		t.Fatalf("create_site: %v", s)
	}
	same(t, local, web, "create_site", map[string]any{"title": "Shop", "workspace": "999"})
	v, err := r.env.Svc.CreateSite(ctx, ann, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.env.Svc.Deploy(ctx, ann, v.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "1"})), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	same(t, local, web, "move_site", map[string]any{"site": "promo", "workspace": id})
	wantToolErr(t, local, "move_site", map[string]any{"site": "promo", "workspace": id}, "confirm")
	moved := mustTool(t, web, "move_site", map[string]any{"site": "promo", "workspace": id, "confirm": "promo"})
	if moved["workspace_id"] != id {
		t.Fatalf("move: %v", moved)
	}
	sites := same(t, local, web, "list_sites", map[string]any{})
	if len(sites["sites"].([]any)) != 2 {
		t.Fatalf("sites: %v", sites)
	}
	same(t, local, web, "get_site", map[string]any{"site": "promo"})
	if out := mustTool(t, local, "clone_site", map[string]any{"site": "promo", "workspace": id}); out["site_id"] == "" {
		t.Fatalf("clone: %v", out)
	}
	if out := mustTool(t, web, "create_site_from_template", map[string]any{"template": "links", "workspace": id}); out["site_id"] == "" {
		t.Fatalf("template: %v", out)
	}
	same(t, local, web, "create_site_from_template", map[string]any{"template": "shop", "workspace": id})
	back := mustTool(t, local, "move_site", map[string]any{"site": "promo", "confirm": "promo"})
	if back["workspace_id"] != nil {
		t.Fatalf("move back: %v", back)
	}

	// Removing a member needs the same e-mail in confirm.
	same(t, local, web, "remove_workspace_member", map[string]any{"workspace": id, "email": "bob@pp.test"})
	same(t, local, web, "remove_workspace_member", map[string]any{"workspace": id, "email": "eve@pp.test", "confirm": "eve@pp.test"})
	out := mustTool(t, local, "remove_workspace_member", map[string]any{"workspace": id, "email": "bob@pp.test", "confirm": "BOB@pp.test"})
	if out["workspace_deleted"] != false {
		t.Fatalf("remove: %v", out)
	}
	wantToolErr(t, web, "remove_workspace_member", map[string]any{"workspace": id, "email": "ann@pp.test", "confirm": "ann@pp.test"},
		domain.CodeWorkspaceLastMember)
}
