package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func secretSite(t *testing.T) string {
	t.Helper()
	return siteDir(t, map[string]string{
		"index.html": "<h1>hi</h1>", ".env": "SECRET=1", ".well-known/security.txt": "Contact: x", "keys/server.key": "k",
	})
}

func TestDeployRefusesSecretFiles(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := secretSite(t)
	before := e.srv.requestCount()
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "look like secrets", ".env", "keys/server.key", "--skip-sensitive", "--include-sensitive")
	if e.srv.requestCount() != before {
		t.Fatal("refused deploy made network requests")
	}

	r = e.run("deploy", dir, "--skip-sensitive")
	expectCode(t, r, 0)
	expectContains(t, "stderr", r.stderr, "Not publishing 2 secret-looking file(s)")
	if !slices.Equal(e.srv.lastFiles, []string{".well-known/security.txt", "index.html"}) {
		t.Fatalf("uploaded files = %v", e.srv.lastFiles)
	}

	r = e.run("deploy", dir, "--include-sensitive")
	expectCode(t, r, 0)
	if !slices.Contains(e.srv.lastFiles, ".env") {
		t.Fatalf("uploaded files = %v", e.srv.lastFiles)
	}
	expectCode(t, e.run("deploy", dir, "--skip-sensitive", "--include-sensitive"), 2)
}

func TestDeployFolderGuards(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	cs := mcpClient(t, e.app())

	write := func(dir string, files map[string]string) {
		for name, body := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(e.home, map[string]string{"index.html": "x", "Documents/index.html": "x", "Documents/site/dist/index.html": "x"})
	for _, p := range []string{e.home, "~", "~/Documents", filepath.Join(e.home, "Documents") + "/", string(os.PathSeparator)} {
		if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": p}); !strings.Contains(msg, "home folder") {
			t.Fatalf("%s: %q", p, msg)
		}
	}
	// A symbolic link to the home folder is resolved.
	link := filepath.Join(t.TempDir(), "h")
	if err := os.Symlink(e.home, link); err == nil {
		if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": link}); !strings.Contains(msg, "home folder") {
			t.Fatalf("symlink: %q", msg)
		}
	}
	if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": "~/Documents/site/dist"}); msg != "" {
		t.Fatalf("nested folder: %q", msg)
	}

	dir := secretSite(t)
	_, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": dir})
	if !strings.Contains(msg, ".env") || !strings.Contains(msg, "skip_sensitive_files") || strings.Contains(msg, "--include") {
		t.Fatalf("secrets: %q", msg)
	}
	if _, msg := callTool(t, cs, "deploy_folder", map[string]any{"path": dir, "skip_sensitive_files": true}); msg != "" {
		t.Fatalf("skip: %q", msg)
	}
	if slices.Contains(e.srv.lastFiles, ".env") || !slices.Contains(e.srv.lastFiles, ".well-known/security.txt") {
		t.Fatalf("uploaded files = %v", e.srv.lastFiles)
	}
}
