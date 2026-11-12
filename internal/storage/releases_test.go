package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStagingCommitDiscard(t *testing.T) {
	f, root := newTestFS(t)
	dir, err := f.StagingDir(siteA, deploy1)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "sites", siteA, "releases", deploy1+".tmp")
	if dir != want {
		t.Fatalf("staging = %s, want %s", dir, want)
	}
	if exists(t, dir) {
		t.Fatal("staging dir must not be created")
	}
	if !exists(t, filepath.Dir(dir)) {
		t.Fatal("releases dir must be created")
	}
	if err := f.CommitRelease(siteA, deploy1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("commit without staging: %v", err)
	}

	makeRelease(t, f, siteA, deploy1, "x")
	if exists(t, dir) {
		t.Fatal("staging still present after commit")
	}
	if got := readFile(t, filepath.Join(root, "sites", siteA, "releases", deploy1, "index.html")); got != "x" {
		t.Fatalf("release content %q", got)
	}

	// Committing over an existing release fails and keeps it.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.CommitRelease(siteA, deploy1); !errors.Is(err, ErrExists) {
		t.Fatalf("commit over release: %v", err)
	}
	if err := f.DiscardStaging(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if exists(t, dir) {
		t.Fatal("staging not discarded")
	}
	if err := f.DiscardStaging(siteA, deploy1); err != nil {
		t.Fatalf("discard of absent staging: %v", err)
	}
	if !exists(t, filepath.Join(root, "sites", siteA, "releases", deploy1)) {
		t.Fatal("discard removed the release")
	}
}

func TestDeleteRelease(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "1")
	makeRelease(t, f, siteA, deploy2, "2")
	// Without current any release may be deleted; absent is fine.
	if err := f.DeleteRelease(siteA, deploy3); err != nil {
		t.Fatal(err)
	}
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteRelease(siteA, deploy1); !errors.Is(err, ErrCurrentRelease) {
		t.Fatalf("delete current: %v", err)
	}
	if err := f.DeleteRelease(siteA, deploy2); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(root, "sites", siteA, "releases", deploy2)) {
		t.Fatal("release not deleted")
	}
	if got := readFile(t, filepath.Join(root, "sites", siteA, "current", "index.html")); got != "1" {
		t.Fatalf("current damaged: %q", got)
	}
	// After switching to empty the old release can go.
	if err := f.ActivateEmpty(siteA); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	// current that is not a link makes the check fail loudly.
	if err := os.Remove(filepath.Join(root, "sites", siteA, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sites", siteA, "current"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteRelease(siteA, deploy1); err == nil {
		t.Fatal("expected error when current is not a link")
	}
}

func TestDeleteSiteKeepsOthers(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "a")
	makeRelease(t, f, siteB, deploy1, "b")
	for _, s := range []struct{ id, host string }{{siteA, hostA}, {siteB, hostB}} {
		if err := f.ActivateRelease(s.id, deploy1); err != nil {
			t.Fatal(err)
		}
		if err := f.LinkHost(s.host, s.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.DeleteSite(siteA); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(root, "sites", siteA)) {
		t.Fatal("site not deleted")
	}
	if got := readFile(t, filepath.Join(root, "by-host", hostB, "index.html")); got != "b" {
		t.Fatalf("other site damaged: %q", got)
	}
	if !exists(t, filepath.Join(root, "empty")) {
		t.Fatal("empty dir removed")
	}
	if err := f.DeleteSite(siteA); err != nil {
		t.Fatalf("delete absent site: %v", err)
	}
}

func TestSiteSize(t *testing.T) {
	f, root := newTestFS(t)
	if _, err := f.SiteSize(siteA, deploy1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release: %v", err)
	}
	makeRelease(t, f, siteA, deploy1, "12345")
	rel := filepath.Join(root, "sites", siteA, "releases", deploy1)
	if err := os.MkdirAll(filepath.Join(rel, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "css", "a.css"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlinks are not followed or counted.
	if err := os.Symlink("../../../../../empty", filepath.Join(rel, "link")); err != nil {
		t.Fatal(err)
	}
	n, err := f.SiteSize(siteA, deploy1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Fatalf("size = %d, want 8", n)
	}
}

func TestCopyRelease(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "index")
	src := filepath.Join(root, "sites", siteA, "releases", deploy1)
	if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a", "b", "c.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, p := range []string{"index.html", "a/b/c.css", "a/b", "a"} {
		if err := os.Chtimes(filepath.Join(src, p), old, old); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.CopyRelease(siteA, deploy1, deploy2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Bytes != int64(len("index")+len("body{}")) {
		t.Fatalf("result %+v", res)
	}
	staging := filepath.Join(root, "sites", siteA, "releases", deploy2+".tmp")
	for _, p := range []string{"index.html", "a/b/c.css", "a"} {
		fi, err := os.Stat(filepath.Join(staging, p))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(old) {
			t.Errorf("%s mtime %s, want %s", p, fi.ModTime(), old)
		}
		want := os.FileMode(0o644)
		if fi.IsDir() {
			want = 0o755
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode %v", p, fi.Mode())
		}
	}
	if got := readFile(t, filepath.Join(staging, "a", "b", "c.css")); got != "body{}" {
		t.Fatalf("content %q", got)
	}
	if err := f.CommitRelease(siteA, deploy2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CopyRelease(siteA, deploy3, "20261112-101533-qqq111"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source: %v", err)
	}
	if _, err := f.CopyRelease(siteA, "../x", deploy3); err == nil {
		t.Fatal("invalid id accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, "sites", siteA, "releases", deploy3+".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.CopyRelease(siteA, deploy1, deploy3); !errors.Is(err, ErrExists) {
		t.Fatalf("existing staging: %v", err)
	}
}
