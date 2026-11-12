package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestDeployFirstCreatesSiteAndProjectFile(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, map[string]string{"index.html": "<h1>hi</h1>", "css/a.css": "a{}", ".git/HEAD": "ref"})
	r := e.run("deploy", dir)
	expectCode(t, r, 0)
	if r.stdout != "https://site01.sites.test\n" {
		t.Fatalf("stdout = %q, want exactly the URL", r.stdout)
	}
	expectContains(t, "stderr", r.stderr, "Created site site01", "Published 2 files")

	p, ok, err := readProject(dir)
	if err != nil || !ok || p.SiteID != "site01" || p.Server != e.srv.URL {
		t.Fatalf("project = %+v ok=%v err=%v", p, ok, err)
	}
	if !slices.Equal(e.srv.lastFiles, []string{"css/a.css", "index.html"}) {
		t.Fatalf("uploaded files = %v", e.srv.lastFiles)
	}
	if e.srv.sites["site01"].Title != "my-site" {
		t.Fatalf("title = %q, want directory name", e.srv.sites["site01"].Title)
	}
}

func TestDeployRepeatUpdatesSameSite(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, nil)
	first := e.run("deploy", dir)
	expectCode(t, first, 0)
	second := e.run("deploy", dir)
	expectCode(t, second, 0)
	if first.stdout != second.stdout {
		t.Fatalf("URL changed: %q vs %q", first.stdout, second.stdout)
	}
	if e.srv.siteCount() != 1 || len(e.srv.sites["site01"].Deploys) != 2 {
		t.Fatalf("sites=%d deploys=%d", e.srv.siteCount(), len(e.srv.sites["site01"].Deploys))
	}
	if slices.Contains(e.srv.lastFiles, ".dropsite.json") {
		t.Fatalf("project file was published: %v", e.srv.lastFiles)
	}
}

func TestDeployWithoutIndexFailsBeforeNetwork(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	before := e.srv.requestCount()
	dir := siteDir(t, map[string]string{"about.html": "x"})
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "has no index.html")
	if e.srv.requestCount() != before {
		t.Fatal("deploy made network requests for a directory without index.html")
	}
}

func TestDeployMissingDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	r := e.run("deploy", filepath.Join(t.TempDir(), "nope"))
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "cannot open directory")

	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o644)
	expectContains(t, "stderr", e.run("deploy", file).stderr, "is not a directory")
}

func TestDeployIndexIsDirectory(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, map[string]string{"index.html/x": "x"})
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "regular file")
}

func TestDeployWithoutLogin(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("deploy", siteDir(t, nil))
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "not logged in", "dropsite login")
	if e.srv.requestCount() != 0 {
		t.Fatal("unexpected requests")
	}
}

func TestDeployJSON(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	r := e.run("deploy", siteDir(t, nil), "--json")
	expectCode(t, r, 0)
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	if dec.More() {
		t.Fatal("stdout has more than one JSON value")
	}
	if out["site_id"] != "site01" || out["url"] != "https://site01.sites.test" || out["deploy_id"] == "" {
		t.Fatalf("json = %v", out)
	}
	if urls, ok := out["urls"].([]any); !ok || len(urls) != 1 {
		t.Fatalf("urls = %v", out["urls"])
	}
}

func TestDeployServerErrorGoesToStderr(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.deployErr = &apiErr{413, "archive_too_large", "unpacked size exceeds the limit", map[string]any{"limit": 1024}}
	r := e.run("deploy", siteDir(t, nil))
	expectCode(t, r, 1)
	if r.stdout != "" {
		t.Fatalf("stdout must be empty on failure: %q", r.stdout)
	}
	expectContains(t, "stderr", r.stderr, "unpacked size exceeds the limit (archive_too_large)", "limit: 1024")
}

func TestDeployExplicitSiteBySlug(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.addSite("Promo", "promo", 0)
	dir := siteDir(t, nil)
	r := e.run("deploy", "--site", "promo", dir)
	expectCode(t, r, 0)
	if r.stdout != "https://promo.sites.test\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if _, ok, _ := readProject(dir); ok {
		t.Fatal("--site must not write a project file")
	}
}

func TestDeployExplicitSiteNotFound(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	r := e.run("deploy", siteDir(t, nil), "--site", "ghost")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "site ghost", "dropsite sites")
	if e.srv.siteCount() != 0 {
		t.Fatal("a site was created")
	}
}

func TestDeployLinkedSiteDeleted(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, nil)
	expectCode(t, e.run("deploy", dir), 0)
	expectCode(t, e.run("delete", "site01", "--yes"), 0)
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "site01 from .dropsite.json", "Delete .dropsite.json", "--site")
	if e.srv.siteCount() != 0 {
		t.Fatal("a new site was created silently")
	}
}

func TestDeployLinkedToOtherServer(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, nil)
	if err := writeProject(dir, project{Server: "https://other.example.com", SiteID: "x"}); err != nil {
		t.Fatal(err)
	}
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "https://other.example.com", "you are logged in to")
	if e.srv.sawRequest("POST /api/v1/sites") {
		t.Fatal("must not create or upload")
	}
}

func TestDeployBadProjectFile(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, map[string]string{"index.html": "x", ".dropsite.json": "{}"})
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "not a valid dropsite project file")
}

func TestDeployCustomTitle(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	expectCode(t, e.run("deploy", "--title", "Launch", siteDir(t, nil)), 0)
	if e.srv.sites["site01"].Title != "Launch" {
		t.Fatalf("title = %q", e.srv.sites["site01"].Title)
	}
}

func TestDeployEmptyTitleUsesDirName(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	expectCode(t, e.run("deploy", "--title", "", siteDir(t, nil)), 0)
	if e.srv.sites["site01"].Title != "my-site" {
		t.Fatalf("title = %q", e.srv.sites["site01"].Title)
	}
}

func TestDeploySymlinkRejectedWithoutUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	e := newTestEnv(t)
	e.login()
	dir := siteDir(t, nil)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "leak")); err != nil {
		t.Fatal(err)
	}
	r := e.run("deploy", dir)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "archive_link", "path: leak")
	if e.srv.sawRequest("POST /api/v1/sites") {
		t.Fatal("no site may be created or uploaded for an invalid directory")
	}
}

func TestDeployTooManyFilesForLimits(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	files := map[string]string{"index.html": "x"}
	for i := range 101 {
		files[filepath.Join("f", strings.Repeat("a", 1)+string(rune('a'+i%26))+string(rune('a'+i/26)))] = "x"
	}
	r := e.run("deploy", siteDir(t, files))
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "archive_too_many_files")
}

func TestDeployUsage(t *testing.T) {
	e := newTestEnv(t)
	expectCode(t, e.run("deploy"), 2)
	expectCode(t, e.run("deploy", "a", "b"), 2)
	r := e.run("deploy", "-h")
	expectCode(t, r, 0)
	expectContains(t, "stderr", r.stderr, "Usage: dropsite deploy", "-site")
}

func TestDeployRateLimited(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.srv.rateLimit = true
	r := e.run("deploy", siteDir(t, nil))
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "rate_limited", "Retry in 42 seconds")
}
