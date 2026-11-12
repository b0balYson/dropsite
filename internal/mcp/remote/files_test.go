package remote

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRemoteFileToolsListed(t *testing.T) {
	r := newRig(t)
	cs := r.session(r.env.Token(r.env.User("alice@pp.test"), "mcp"))
	res, err := cs.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*sdk.Tool{}
	for _, tl := range res.Tools {
		names[tl.Name] = tl
	}
	for _, n := range []string{"list_site_files", "read_site_files", "update_site_files", "begin_upload", "upload_files", "commit_upload"} {
		if names[n] == nil {
			t.Fatalf("tool %s missing", n)
		}
	}
	if !names["list_site_files"].Annotations.ReadOnlyHint || names["update_site_files"].Annotations.ReadOnlyHint {
		t.Fatal("annotations")
	}
	if !strings.Contains(names["deploy_files"].Description, "update_site_files") {
		t.Fatal("deploy_files does not point to update_site_files")
	}
}

func TestRemoteEditSiteFiles(t *testing.T) {
	r := newRig(t)
	alice := r.env.User("alice@pp.test")
	cs := r.session(r.env.Token(alice, "mcp"))
	out, msg := call(t, cs, "deploy_files", map[string]any{"title": "Cafe", "files": []map[string]string{
		{"path": "index.html", "content": "<p>Coffee 100</p>"},
		{"path": "css/site.css", "content": "p{}"},
		{"path": "img/logo.png", "content": base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0}), "encoding": "base64"},
	}})
	if msg != "" {
		t.Fatal(msg)
	}
	site := out["site_id"].(string)

	out, msg = call(t, cs, "list_site_files", map[string]any{"site": site})
	if msg != "" || out["total_files"].(float64) != 3 || out["current"] != true {
		t.Fatalf("list: %v %q", out, msg)
	}
	base := out["deploy_id"].(string)

	out, msg = call(t, cs, "read_site_files", map[string]any{"site": site, "paths": []string{"index.html", "img/*"}})
	if msg != "" || out["deploy_id"] != base {
		t.Fatalf("read: %v %q", out, msg)
	}
	files := out["files"].([]any)
	first, second := files[0].(map[string]any), files[1].(map[string]any)
	if first["content"] != "<p>Coffee 100</p>" || first["encoding"] != "utf8" || second["encoding"] != "base64" {
		t.Fatalf("read files: %v", files)
	}
	if _, msg := call(t, cs, "read_site_files", map[string]any{"site": site, "paths": []string{"../../state.db"}}); msg == "" {
		t.Fatal("traversal accepted")
	}

	r.env.Clock.Advance(time.Second)
	out, msg = call(t, cs, "update_site_files", map[string]any{"site": site, "base_deploy_id": base,
		"files":  []map[string]string{{"path": "index.html", "content": "<p>Coffee 120</p>"}},
		"delete": []string{"img/logo.png"}})
	if msg != "" || out["deleted"].(float64) != 1 || out["file_count"].(float64) != 2 || !strings.Contains(out["summary"].(string), "rollback") {
		t.Fatalf("update: %v %q", out, msg)
	}
	host := strings.TrimSuffix(strings.TrimPrefix(out["url"].(string), "https://"), "/")
	if got, _ := r.env.Served(host, "css/site.css"); got != "p{}" {
		t.Fatalf("unchanged file: %q", got)
	}
	// The same base again is stale now.
	_, msg = call(t, cs, "update_site_files", map[string]any{"site": site, "base_deploy_id": base,
		"files": []map[string]string{{"path": "index.html", "content": "x"}}})
	if !strings.Contains(msg, "deploy_conflict") || !strings.Contains(msg, "list_site_files") {
		t.Fatalf("conflict: %q", msg)
	}
	// Another user sees nothing.
	bs := r.session(r.env.Token(r.env.User("bob@pp.test"), "mcp"))
	if _, msg := call(t, bs, "list_site_files", map[string]any{"site": site}); !strings.Contains(msg, "not found") {
		t.Fatalf("foreign: %q", msg)
	}
}

func TestRemoteUploadSession(t *testing.T) {
	r := newRig(t)
	cs := r.session(r.env.Token(r.env.User("alice@pp.test"), "mcp"))
	out, msg := call(t, cs, "begin_upload", map[string]any{"title": "Big"})
	if msg != "" || out["mode"] != "replace" || out["max_call_bytes"].(float64) != 16<<20 {
		t.Fatalf("begin: %v %q", out, msg)
	}
	id := out["upload_id"].(string)
	part := strings.Repeat("z", 700<<10)
	for _, p := range []string{"index.html", "a.txt"} {
		if out, msg = call(t, cs, "upload_files", map[string]any{"upload_id": id,
			"files": []map[string]string{{"path": p, "content": part}}}); msg != "" {
			t.Fatal(msg)
		}
	}
	if out["files"].(float64) != 2 {
		t.Fatalf("progress: %v", out)
	}
	out, msg = call(t, cs, "commit_upload", map[string]any{"upload_id": id})
	if msg != "" || out["file_count"].(float64) != 2 || out["url"] == "" {
		t.Fatalf("commit: %v %q", out, msg)
	}
	if _, msg := call(t, cs, "upload_files", map[string]any{"upload_id": id,
		"files": []map[string]string{{"path": "b.txt", "content": "b"}}}); !strings.Contains(msg, "upload_expired") {
		t.Fatalf("closed session: %q", msg)
	}
}
