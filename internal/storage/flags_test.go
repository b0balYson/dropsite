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
