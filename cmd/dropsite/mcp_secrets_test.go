package main

import (
	"strings"
	"testing"
)

// fakeGitHubToken is assembled at run time so the repository holds no
// string that looks like a real token.
func fakeGitHubToken() string { return "gh" + "p_" + strings.Repeat("aB3dE5fG7hJ9kL2mN4pQ", 2)[:36] }

func TestStdioSecretsRefusalMatchesRemote(t *testing.T) {
	r := newParityRig(t)
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	key := fakeGitHubToken()

	dir := siteDir(t, map[string]string{"index.html": "<h1>Shop</h1>"})
	mustTool(t, local, "deploy_folder", map[string]any{"path": dir, "title": "Shop"})
	list := mustTool(t, web, "list_sites", map[string]any{})
	site := list["sites"].([]any)[0].(map[string]any)["id"].(string)
	files := same(t, local, web, "list_site_files", map[string]any{"site": site})

	upd := map[string]any{"site": site, "base_deploy_id": files["deploy_id"],
		"files": []map[string]string{{"path": "js/app.js", "content": "// app\nconst token = '" + key + "'"}}}
	_, msg := outputs(t, local, "update_site_files", upd)
	if !strings.Contains(msg, "[secrets_found]") || !strings.Contains(msg, "js/app.js:2 (GitHub token)") ||
		strings.Contains(msg, key) {
		t.Fatalf("update_site_files: %q", msg)
	}
	same(t, local, web, "update_site_files", upd)

	// Publishing a folder (stdio) and files (remote) is refused alike, even
	// when secret-looking file names are allowed.
	bad := siteDir(t, map[string]string{"index.html": "<h1>x</h1>", "config.js": key})
	_, folderMsg := outputs(t, local, "deploy_folder", map[string]any{"path": bad, "site": site})
	_, filesMsg := outputs(t, web, "deploy_files", map[string]any{"site": site,
		"files": []map[string]string{{"path": "index.html", "content": "<h1>x</h1>"}, {"path": "config.js", "content": key}}})
	if folderMsg != filesMsg || !strings.Contains(folderMsg, "config.js:1 (GitHub token)") {
		t.Fatalf("refusals differ:\nstdio  %q\nremote %q", folderMsg, filesMsg)
	}
}
