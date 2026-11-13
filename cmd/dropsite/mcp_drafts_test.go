package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestStdioDraftTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe")
	if err != nil {
		t.Fatal(err)
	}
	live, err := r.env.Svc.Deploy(ctx, ann, v.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "live"})), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
	preview := "https://p-" + v.Site.ID + ".pp.test/"

	// Without a draft both servers answer no_draft alike.
	same(t, local, web, "publish_draft", map[string]any{"site": "cafe"})
	wantToolErr(t, local, "publish_draft", map[string]any{"site": "cafe"}, "no_draft")

	// A change saved as a draft through the local server.
	r.env.Clock.Advance(time.Second)
	out := mustTool(t, local, "update_site_files", map[string]any{"site": "cafe", "base_deploy_id": live.Deploy.ID,
		"draft": true, "files": []map[string]string{{"path": "index.html", "content": "draft"}}})
	if out["draft"] != true || out["preview_url"] != preview || !strings.HasPrefix(out["summary"].(string), "Draft saved") ||
		!strings.Contains(out["summary"].(string), preview) {
		t.Fatalf("draft update: %v", out)
	}
	site := same(t, local, web, "get_site", map[string]any{"site": "cafe"})
	draft, _ := site["draft"].(map[string]any)
	if draft == nil || draft["preview_url"] != preview || site["current_deploy"].(map[string]any)["id"] != live.Deploy.ID {
		t.Fatalf("get_site: %v", site)
	}
	ds := same(t, local, web, "list_deploys", map[string]any{"site": "cafe"})
	if first := ds["deploys"].([]any)[0].(map[string]any); first["draft"] != true || first["id"] != draft["deploy_id"] {
		t.Fatalf("list_deploys: %v", ds)
	}

	// discard_draft needs the user's confirmation.
	same(t, local, web, "discard_draft", map[string]any{"site": "cafe", "confirm": ""})
	wantToolErr(t, web, "discard_draft", map[string]any{"site": "cafe"}, "confirm")

	pub := mustTool(t, web, "publish_draft", map[string]any{"site": "cafe"})
	if pub["deploy_id"] != draft["deploy_id"] || pub["draft"] == true {
		t.Fatalf("publish_draft: %v", pub)
	}

	// A draft from files by value (remote) and from an upload (local), then
	// discarded with confirmation.
	r.env.Clock.Advance(time.Second)
	folder := mustTool(t, local, "deploy_folder", map[string]any{"site": "cafe", "draft": true,
		"path": siteDir(t, map[string]string{"index.html": "folder draft"})})
	if folder["draft"] != true || folder["preview_url"] != preview {
		t.Fatalf("deploy_folder draft: %v", folder)
	}
	r.env.Clock.Advance(time.Second)
	res := mustTool(t, web, "deploy_files", map[string]any{"site": "cafe", "draft": true,
		"files": []map[string]string{{"path": "index.html", "content": "files draft"}}})
	if res["draft"] != true || res["preview_url"] != preview {
		t.Fatalf("deploy_files draft: %v", res)
	}
	r.env.Clock.Advance(time.Second)
	up := mustTool(t, local, "begin_upload", map[string]any{"site": "cafe"})
	mustTool(t, local, "upload_files", map[string]any{"upload_id": up["upload_id"],
		"files": []map[string]string{{"path": "index.html", "content": "upload draft"}}})
	out = mustTool(t, local, "commit_upload", map[string]any{"upload_id": up["upload_id"], "draft": true})
	if out["draft"] != true || out["preview_url"] != preview {
		t.Fatalf("commit_upload draft: %v", out)
	}
	if got, _ := r.env.Served("p-"+v.Site.ID+".pp.test", "index.html"); got != "upload draft" {
		t.Fatalf("preview serves %q", got)
	}
	gone := mustTool(t, local, "discard_draft", map[string]any{"site": "cafe", "confirm": "cafe"})
	if gone["draft"] != nil {
		t.Fatalf("discard_draft: %v", gone)
	}
	same(t, local, web, "discard_draft", map[string]any{"site": "cafe", "confirm": "cafe"})

	// Strangers see nothing.
	bob := r.env.Token(r.env.User("bob@pp.test"), "cli")
	bl, bw := r.stdio(bob), r.web(bob)
	same(t, bl, bw, "publish_draft", map[string]any{"site": v.Site.ID})
	wantToolErr(t, bl, "publish_draft", map[string]any{"site": v.Site.ID}, "not found")
}
