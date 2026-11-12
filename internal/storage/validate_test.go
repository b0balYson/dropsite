package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var badIDs = []string{"", ".", "..", "../x", "a/b", "../../etc", "AAAAA11111", "aaaaa1111", "aaaaa111111", "aaaaa1111/", " aaaaa1111"}

var badDeployIDs = []string{"", "..", "../x", "a/b", "20261112-101530-X7K2M9", "20261112-101530-x7k2m", "20261112-101530-x7k2m9.tmp", "20261112-101530-x7k2m9/..", "../20261112-101530-x7k2m9"}

var badHosts = []string{"", ".", "..", "../x", "a/b", "A.example.com", "../sites", "%2e%2e", "a..b", "1.2.3.4", "10.0.0.1", ".tmplink-00"}

func TestValidators(t *testing.T) {
	for _, id := range badIDs {
		if err := validateSiteID(id); !errors.Is(err, ErrInvalidName) {
			t.Errorf("site id %q accepted", id)
		}
	}
	for _, id := range badDeployIDs {
		if err := validateDeployID(id); !errors.Is(err, ErrInvalidName) {
			t.Errorf("deploy id %q accepted", id)
		}
	}
	for _, h := range badHosts {
		if err := validateHostname(h); !errors.Is(err, ErrInvalidName) {
			t.Errorf("hostname %q accepted", h)
		}
	}
	for _, h := range []string{"example.com", "a", hostA, "x1.y2", "1a.com"} {
		if err := validateHostname(h); err != nil {
			t.Errorf("hostname %q rejected: %v", h, err)
		}
	}
	if err := validateSiteAndDeploy(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
}

// TestMethodsRejectDangerousNames calls every method with dangerous names and
// checks that each fails with ErrInvalidName and leaves the disk untouched.
func TestMethodsRejectDangerousNames(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "a")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkHost(hostA, siteA); err != nil {
		t.Fatal(err)
	}
	// A sibling outside the root that a traversal would hit.
	outside := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })

	calls := map[string]func() error{}
	for _, bad := range badIDs {
		calls["staging site "+bad] = func() error { _, err := f.StagingDir(bad, deploy1); return err }
		calls["commit site "+bad] = func() error { return f.CommitRelease(bad, deploy1) }
		calls["discard site "+bad] = func() error { return f.DiscardStaging(bad, deploy1) }
		calls["activate site "+bad] = func() error { return f.ActivateRelease(bad, deploy1) }
		calls["empty site "+bad] = func() error { return f.ActivateEmpty(bad) }
		calls["link site "+bad] = func() error { return f.LinkHost(hostA, bad) }
		calls["delrel site "+bad] = func() error { return f.DeleteRelease(bad, deploy1) }
		calls["delsite "+bad] = func() error { return f.DeleteSite(bad) }
		calls["size site "+bad] = func() error { _, err := f.SiteSize(bad, deploy1); return err }
	}
	for _, bad := range badDeployIDs {
		calls["staging deploy "+bad] = func() error { _, err := f.StagingDir(siteA, bad); return err }
		calls["commit deploy "+bad] = func() error { return f.CommitRelease(siteA, bad) }
		calls["discard deploy "+bad] = func() error { return f.DiscardStaging(siteA, bad) }
		calls["activate deploy "+bad] = func() error { return f.ActivateRelease(siteA, bad) }
		calls["delrel deploy "+bad] = func() error { return f.DeleteRelease(siteA, bad) }
		calls["size deploy "+bad] = func() error { _, err := f.SiteSize(siteA, bad); return err }
	}
	for _, bad := range badHosts {
		calls["link host "+bad] = func() error { return f.LinkHost(bad, siteA) }
		calls["unlink host "+bad] = func() error { return f.UnlinkHost(bad) }
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%s: want ErrInvalidName, got %v", name, err)
		}
	}

	if got := readFile(t, filepath.Join(root, "by-host", hostA, "index.html")); got != "a" {
		t.Fatalf("site damaged: %q", got)
	}
	if !exists(t, outside) {
		t.Fatal("directory outside root removed")
	}
	entries, err := os.ReadDir(filepath.Join(root, "sites"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected sites entries: %v %v", entries, err)
	}
}

func TestLinkTargetValidators(t *testing.T) {
	const site, deploy = "abcdefghij", "20261112-100000-abc123"
	cases := []struct {
		fn     func(string) bool
		target string
		want   bool
	}{
		{CurrentTargetValid, releaseTarget(deploy), true},
		{CurrentTargetValid, emptyTarget, true},
		{CurrentTargetValid, "releases/r1", false},
		{CurrentTargetValid, "releases/" + deploy + "/..", false},
		{CurrentTargetValid, "../../empty/", false},
		{HostTargetValid, hostTarget(site), true},
		{HostTargetValid, "../sites/" + site + "/releases", false},
		{HostTargetValid, "../sites/../current", false},
		{HostTargetValid, "/sites/" + site + "/current", false},
		{HostTargetValid, previewTarget(site, deploy), true},
		{HostTargetValid, "../sites/" + site + "/releases/" + deploy + "/..", false},
		{HostTargetValid, "../sites/" + site + "/releases/x", false},
		{HostTargetValid, "../sites/" + site + "/releases/" + deploy + ".tmp", false},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.target); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.target, got, tc.want)
		}
	}
	if !ValidSiteID(site) || ValidSiteID("ABC") || !ValidDeployID(deploy) || ValidDeployID("x") ||
		!ValidHostname("a.pp.test") || ValidHostname("10.0.0.1") {
		t.Fatal("exported validators disagree with the internal ones")
	}
}
