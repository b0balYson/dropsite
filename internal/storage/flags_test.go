package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func flagPath(root string, flag HostFlag, host string) string {
	return filepath.Join(root, "flags", string(flag), host)
}

func TestInitCreatesFlagDirs(t *testing.T) {
	_, root := newTestFS(t)
	for _, f := range AllHostFlags {
		fi, err := os.Stat(filepath.Join(root, "flags", string(f)))
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

func TestSetAndClearHostFlag(t *testing.T) {
	f, root := newTestFS(t)
	for _, flag := range AllHostFlags {
		if err := f.SetHostFlag(flag, hostA); err != nil {
			t.Fatal(err)
		}
		// Setting twice is fine and leaves a single regular empty file.
		if err := f.SetHostFlag(flag, hostA); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Lstat(flagPath(root, flag, hostA))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.Mode().IsRegular() || fi.Size() != 0 || fi.Mode().Perm() != 0o644 {
			t.Fatalf("%s marker: mode %v size %d", flag, fi.Mode(), fi.Size())
		}
		if err := f.ClearHostFlag(flag, hostA); err != nil {
			t.Fatal(err)
		}
		if exists(t, flagPath(root, flag, hostA)) {
			t.Fatalf("%s marker still present", flag)
		}
		// Clearing a missing marker is not an error.
		if err := f.ClearHostFlag(flag, hostA); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "flags", string(FlagNoindex)))
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftover entries %v %v", entries, err)
	}
}

func TestSetHostFlagReplacesNonFile(t *testing.T) {
	f, root := newTestFS(t)
	mkdir(t, filepath.Join(flagPath(root, FlagProtected, hostA), "sub"))
	if err := f.SetHostFlag(FlagProtected, hostA); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(flagPath(root, FlagProtected, hostA))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("marker: %v %v", fi, err)
	}
}

func TestHostFlagRejectsBadInput(t *testing.T) {
	f, root := newTestFS(t)
	for _, host := range []string{"", "..", "../sites", "a/b", "10.0.0.1", "UPPER.example.com", ".tmpflag-x"} {
		if err := f.SetHostFlag(FlagNoindex, host); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("set %q: %v", host, err)
		}
		if err := f.ClearHostFlag(FlagNoindex, host); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("clear %q: %v", host, err)
		}
	}
	if err := f.SetHostFlag(HostFlag("other"), hostA); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("unknown flag: %v", err)
	}
	if exists(t, filepath.Join(root, "flags", "other")) {
		t.Fatal("unknown flag directory created")
	}
}

func TestReconcileFlags(t *testing.T) {
	f, root := newTestFS(t)
	// Stale and stray entries in both flag directories.
	write(t, flagPath(root, FlagNoindex, "gone.example.com"))
	write(t, flagPath(root, FlagProtected, hostB)) // site B is no longer protected
	write(t, flagPath(root, FlagNoindex, ".tmpflag-dead"))
	mkdir(t, filepath.Join(flagPath(root, FlagProtected, hostA), "x")) // wrong type
	write(t, filepath.Join(root, "flags", "unknown"))

	desired := []SiteLayout{
		{SiteID: siteA, Hostnames: []string{hostA, "promo.example.com"}, Noindex: true, Protected: true},
		{SiteID: siteB, Hostnames: []string{hostB}},
	}
	rep, err := f.Reconcile(context.Background(), desired, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	assertStrings(t, "set flags", rep.SetFlags, []string{
		"noindex/" + hostA, "noindex/promo.example.com",
		"protected/" + hostA, "protected/promo.example.com",
	})
	assertStrings(t, "cleared flags", rep.ClearedFlags, []string{
		"noindex/gone.example.com", "protected/" + hostB,
	})
	assertStrings(t, "removed temp", rep.RemovedTemp, []string{"flags/noindex/.tmpflag-dead"})
	for _, flag := range AllHostFlags {
		for _, h := range []string{hostA, "promo.example.com"} {
			fi, err := os.Lstat(flagPath(root, flag, h))
			if err != nil || !fi.Mode().IsRegular() {
				t.Fatalf("%s/%s: %v", flag, h, err)
			}
		}
	}
	if exists(t, flagPath(root, FlagProtected, hostB)) || exists(t, flagPath(root, FlagNoindex, "gone.example.com")) {
		t.Fatal("stale marker kept")
	}
	// A second pass changes nothing.
	rep, err = f.Reconcile(context.Background(), desired, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Empty() {
		t.Fatalf("second pass not empty: %+v", rep)
	}
}

// A stray by-host link keeps its marker until the link itself is removed:
// if the run stops in between, the host is not served unprotected.
func TestReconcileClearsMarkersAfterLinks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f, root := newTestFS(t)
	const stray = "stray.example.com"
	byHost := filepath.Join(root, "by-host")
	if err := os.Symlink(filepath.Join("..", "sites", siteA, "current"), filepath.Join(byHost, stray)); err != nil {
		t.Fatal(err)
	}
	write(t, flagPath(root, FlagProtected, stray))
	// The stray link cannot be removed: the run fails half-way.
	if err := os.Chmod(byHost, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(byHost, 0o755) })
	desired := []SiteLayout{{SiteID: siteA, Hostnames: []string{hostA}, Protected: true}}
	if _, err := f.Reconcile(context.Background(), desired, Retained{}); err == nil {
		t.Fatal("reconcile succeeded with a read-only by-host")
	}
	if !exists(t, flagPath(root, FlagProtected, stray)) {
		t.Fatal("marker of a still linked stray host removed before the link")
	}
	if !exists(t, flagPath(root, FlagProtected, hostA)) {
		t.Fatal("missing marker not created before links")
	}
	// Once links can be fixed, the stray marker goes too.
	if err := os.Chmod(byHost, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Reconcile(context.Background(), desired, Retained{}); err != nil {
		t.Fatal(err)
	}
	if exists(t, flagPath(root, FlagProtected, stray)) {
		t.Fatal("stray marker kept")
	}
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}

func TestReconcileFlagsCoverPreviews(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "a1")
	preview := "p-" + deploy1 + "-" + siteA + ".example.com"
	desired := []SiteLayout{{
		SiteID: siteA, CurrentDeployID: ptr(deploy1), Hostnames: []string{hostA}, ReadyDeployIDs: []string{deploy1},
		Previews: []Preview{{Hostname: preview, DeployID: deploy1}}, Protected: true,
	}}
	rep, err := f.Reconcile(context.Background(), desired, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	assertStrings(t, "set flags", rep.SetFlags, []string{"protected/" + hostA, "protected/" + preview})
	if !exists(t, flagPath(root, FlagProtected, preview)) {
		t.Fatal("preview marker missing")
	}
}
