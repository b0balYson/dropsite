package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetCertHosts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f := NewFS(root)
	if err := f.Init(ctx); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, certHostsDir)
	// A stray marker, a directory and a symlink are all removed.
	if err := os.WriteFile(filepath.Join(dir, "old.example.com"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "admin.example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link.example.com")); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCertHosts(ctx, []string{"admin.example.com"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "admin.example.com" || !entries[0].Type().IsRegular() {
		t.Fatalf("entries = %v", entries)
	}
	fi, err := os.Stat(filepath.Join(dir, "admin.example.com"))
	if err != nil || fi.Mode().Perm() != flagPerm {
		t.Fatalf("marker mode %v %v", fi, err)
	}
	// Idempotent, then cleared.
	if err := f.SetCertHosts(ctx, []string{"admin.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCertHosts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("not cleared: %v", entries)
	}
}

func TestSetCertHostsRejectsBadNames(t *testing.T) {
	f := NewFS(t.TempDir())
	for _, h := range []string{"..", "../x", "a/b", "1.2.3.4", "", "UPPER.example.com"} {
		if err := f.SetCertHosts(context.Background(), []string{h}); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: %v", h, err)
		}
	}
}
