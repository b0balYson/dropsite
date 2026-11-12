package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRelease(t *testing.T) {
	f, root := newTestFS(t)
	if _, err := f.OpenRelease(siteA, deploy1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release: %v", err)
	}
	if _, err := f.OpenRelease("../x", deploy1); err == nil {
		t.Fatal("bad site id accepted")
	}
	if _, err := f.OpenRelease(siteA, "../../etc"); err == nil {
		t.Fatal("bad deploy id accepted")
	}
	makeRelease(t, f, siteA, deploy1, "hello")
	outside := filepath.Join(root, "secret")
	if err := os.WriteFile(outside, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(root, "sites", siteA, "releases", deploy1)
	if err := os.Symlink(outside, filepath.Join(rel, "link")); err != nil {
		t.Fatal(err)
	}
	r, err := f.OpenRelease(siteA, deploy1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if b, err := r.ReadFile("index.html"); err != nil || string(b) != "hello" {
		t.Fatalf("read: %q %v", b, err)
	}
	if _, err := r.ReadFile("link"); err == nil {
		t.Fatal("symlink escaping the release was followed")
	}
	if _, err := r.ReadFile("../../../secret"); err == nil {
		t.Fatal("traversal allowed")
	}
}
