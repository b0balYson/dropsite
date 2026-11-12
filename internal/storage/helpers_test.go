package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	siteA   = "aaaaa11111"
	siteB   = "bbbbb22222"
	deploy1 = "20261112-101530-x7k2m9"
	deploy2 = "20261112-101531-abc123"
	deploy3 = "20261112-101532-zzz999"
	hostA   = "aaaaa11111.example.com"
	hostB   = "bbbbb22222.example.com"
)

func newTestFS(t *testing.T) (*FS, string) {
	t.Helper()
	root := t.TempDir()
	f := NewFS(root)
	if err := f.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f, root
}

// makeRelease stages, fills and commits a release containing index.html.
func makeRelease(t *testing.T, f *FS, siteID, deployID, content string) {
	t.Helper()
	dir, err := f.StagingDir(siteID, deployID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.CommitRelease(siteID, deployID); err != nil {
		t.Fatal(err)
	}
}

func readlink(t *testing.T, p string) string {
	t.Helper()
	target, err := os.Readlink(p)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") {
		t.Fatalf("link %s has absolute target %q", p, target)
	}
	return target
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ptr(s string) *string { return &s }
