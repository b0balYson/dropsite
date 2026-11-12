package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestInitCreatesLayout(t *testing.T) {
	_, root := newTestFS(t)
	for _, d := range []string{"sites", "by-host", "empty"} {
		fi, err := os.Stat(filepath.Join(root, d))
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", d, err)
		}
		if fi.Mode().Perm()&0o755 != 0o755 {
			t.Fatalf("%s: mode %v", d, fi.Mode())
		}
	}
	// Idempotent.
	if err := NewFS(root).Init(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLinksAreRelativeAndResolve(t *testing.T) {
	f, root := newTestFS(t)
	if err := f.ActivateEmpty(siteA); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkHost(hostA, siteA); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(root, "sites", siteA, "current")); got != "../../empty" {
		t.Fatalf("empty current = %q", got)
	}
	if got := readlink(t, filepath.Join(root, "by-host", hostA)); got != "../sites/"+siteA+"/current" {
		t.Fatalf("by-host = %q", got)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, "by-host", hostA))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(realRoot, "empty") {
		t.Fatalf("empty chain resolves to %s", resolved)
	}

	makeRelease(t, f, siteA, deploy1, "one")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(root, "sites", siteA, "current")); got != "releases/"+deploy1 {
		t.Fatalf("current = %q", got)
	}
	resolved, err = filepath.EvalSymlinks(filepath.Join(root, "by-host", hostA))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(realRoot, "sites", siteA, "releases", deploy1) {
		t.Fatalf("chain resolves to %s", resolved)
	}

	// The layout keeps working when the volume is mounted elsewhere.
	moved := filepath.Join(t.TempDir(), "mnt")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(moved, "by-host", hostA, "index.html")); got != "one" {
		t.Fatalf("after move: %q", got)
	}
	assertNoTempLinks(t, moved)
}

func assertNoTempLinks(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), tempLinkPrefix) {
			t.Errorf("leftover temp link %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestActivateReleaseRequiresDir(t *testing.T) {
	f, root := newTestFS(t)
	if err := f.ActivateRelease(siteA, deploy1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release: %v", err)
	}
	// A symlink in place of a release is not a release.
	if err := os.MkdirAll(filepath.Join(root, "sites", siteA, "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(root, "sites", siteA, "releases", deploy1)); err != nil {
		t.Fatal(err)
	}
	if err := f.ActivateRelease(siteA, deploy1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("symlinked release: %v", err)
	}
}

func TestReplaceSymlinkRejectsAbsolute(t *testing.T) {
	if err := replaceSymlink("/etc", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("absolute target accepted")
	}
}

func TestReplaceSymlinkFailures(t *testing.T) {
	dir := t.TempDir()
	// Missing parent directory: Symlink fails.
	if err := replaceSymlink("x", filepath.Join(dir, "nope", "link")); err == nil {
		t.Fatal("expected error for missing directory")
	}
	// Non-empty directory in place of the link: Rename fails, temp link is cleaned.
	busy := filepath.Join(dir, "busy")
	if err := os.MkdirAll(filepath.Join(busy, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceSymlink("x", busy); err == nil {
		t.Fatal("expected error for directory in place of link")
	}
	assertNoTempLinks(t, dir)
}

func TestUnlinkHostKeepsTarget(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "keep me")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{hostA, "other.example.com"} {
		if err := f.LinkHost(h, siteA); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.UnlinkHost(hostA); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(root, "by-host", hostA)) {
		t.Fatal("host link still present")
	}
	if got := readFile(t, filepath.Join(root, "sites", siteA, "releases", deploy1, "index.html")); got != "keep me" {
		t.Fatalf("release damaged: %q", got)
	}
	if got := readFile(t, filepath.Join(root, "by-host", "other.example.com", "index.html")); got != "keep me" {
		t.Fatalf("other host damaged: %q", got)
	}
	// Absent host is not an error.
	if err := f.UnlinkHost(hostA); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory in place of a host entry cannot be unlinked.
	if err := os.MkdirAll(filepath.Join(root, "by-host", "dir.example.com", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.UnlinkHost("dir.example.com"); err == nil {
		t.Fatal("expected error for directory entry")
	}
}

func TestLinkHostRepointsAtomically(t *testing.T) {
	f, root := newTestFS(t)
	if err := f.ActivateEmpty(siteA); err != nil {
		t.Fatal(err)
	}
	if err := f.ActivateEmpty(siteB); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkHost(hostA, siteA); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkHost(hostA, siteB); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(root, "by-host", hostA)); got != "../sites/"+siteB+"/current" {
		t.Fatalf("by-host = %q", got)
	}
}

// TestActivateUnderLoad switches current 1000 times while readers read
// index.html through by-host; every read must succeed.
func TestActivateUnderLoad(t *testing.T) {
	const (
		readers  = 8
		switches = 1000
	)
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "release-one")
	makeRelease(t, f, siteA, deploy2, "release-two")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkHost(hostA, siteA); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(root, "by-host", hostA, "index.html")

	var (
		stop       atomic.Bool
		failures   atomic.Int64
		seenOne    atomic.Bool
		seenTwo    atomic.Bool
		wg         sync.WaitGroup
		firstError atomic.Value
	)
	for range readers {
		wg.Go(func() {
			for !stop.Load() {
				b, err := os.ReadFile(page)
				switch {
				case err != nil:
					failures.Add(1)
					firstError.CompareAndSwap(nil, err.Error())
				case string(b) == "release-one":
					seenOne.Store(true)
				case string(b) == "release-two":
					seenTwo.Store(true)
				default:
					failures.Add(1)
					firstError.CompareAndSwap(nil, "unexpected content "+string(b))
				}
			}
		})
	}
	for i := range switches {
		d := deploy1
		if i%2 == 0 {
			d = deploy2
		}
		if err := f.ActivateRelease(siteA, d); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if n := failures.Load(); n > 0 {
		// rename(2) over a symlink is atomic for path lookup on Linux, where
		// the service runs. The macOS VFS can return a transient EINVAL
		// while a path component is being replaced, so there it is logged.
		if runtime.GOOS == "linux" {
			t.Fatalf("%d failed reads, first: %v", n, firstError.Load())
		}
		t.Logf("%s: %d transient failed reads (strict on linux), first: %v", runtime.GOOS, n, firstError.Load())
	}
	if !seenOne.Load() || !seenTwo.Load() {
		t.Fatalf("both contents must be seen: one=%v two=%v", seenOne.Load(), seenTwo.Load())
	}
	assertNoTempLinks(t, root)
}

// TestConcurrentSites runs operations on different sites from many goroutines.
func TestConcurrentSites(t *testing.T) {
	f, root := newTestFS(t)
	ids := []string{"site000001", "site000002", "site000003", "site000004"}
	for _, id := range ids {
		makeRelease(t, f, id, deploy1, id)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			for range 50 {
				if err := f.ActivateRelease(id, deploy1); err != nil {
					t.Error(err)
				}
				if err := f.LinkHost(id+".example.com", id); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	for _, id := range ids {
		if got := readFile(t, filepath.Join(root, "by-host", id+".example.com", "index.html")); got != id {
			t.Fatalf("%s: %q", id, got)
		}
	}
}
