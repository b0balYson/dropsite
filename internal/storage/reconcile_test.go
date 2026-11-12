package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	siteKept = "kkkkk33333"
	siteGone = "ggggg44444"
)

// messyLayout builds a disk state that diverges from the database in every
// way Reconcile must handle, and returns the desired state.
func messyLayout(t *testing.T, f *FS, root string) ([]SiteLayout, Retained) {
	t.Helper()
	// siteA: current deploy1, ready deploy1+deploy2; deploy3 is stale, a
	// staging dir and a temp link are left over, current points wrongly.
	makeRelease(t, f, siteA, deploy1, "a1")
	makeRelease(t, f, siteA, deploy2, "a2")
	makeRelease(t, f, siteA, deploy3, "a3")
	if err := f.ActivateRelease(siteA, deploy3); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(root, "sites", siteA, "releases", deploy2+".tmp", "sub"))
	write(t, filepath.Join(root, "sites", siteA, "releases", "junk.txt"))
	symlink(t, "releases/"+deploy1, filepath.Join(root, "sites", siteA, tempLinkPrefix+"dead"))

	// siteB: desired current deploy1, but its directory is gone.
	makeRelease(t, f, siteB, deploy2, "b2")
	if err := f.ActivateRelease(siteB, deploy2); err != nil {
		t.Fatal(err)
	}

	// siteKept: soft-deleted, retained; has a staging dir.
	makeRelease(t, f, siteKept, deploy1, "k1")
	mkdir(t, filepath.Join(root, "sites", siteKept, "releases", deploy2+".tmp"))
	if err := f.ActivateRelease(siteKept, deploy1); err != nil {
		t.Fatal(err)
	}

	// siteGone: neither desired nor retained. Plus junk in sites/.
	makeRelease(t, f, siteGone, deploy1, "g1")
	mkdir(t, filepath.Join(root, "sites", "NOT-A-SITE"))
	write(t, filepath.Join(root, "sites", "stray-file"))

	// by-host: wrong target, stale host, junk, temp link, invalid name.
	symlink(t, "../sites/"+siteGone+"/current", filepath.Join(root, "by-host", hostA))
	symlink(t, "../sites/"+siteGone+"/current", filepath.Join(root, "by-host", "gone.example.com"))
	write(t, filepath.Join(root, "by-host", "junk"))
	mkdir(t, filepath.Join(root, "by-host", "dir.example.com", "x"))
	symlink(t, "x", filepath.Join(root, "by-host", tempLinkPrefix+"dead"))
	symlink(t, filepath.Join(root, "sites", siteKept, "releases", deploy1), filepath.Join(root, "by-host", "BAD..name"))

	return []SiteLayout{
			{SiteID: siteA, CurrentDeployID: ptr(deploy1), Hostnames: []string{hostA, "promo.example.com"}, ReadyDeployIDs: []string{deploy1, deploy2}},
			{SiteID: siteB, CurrentDeployID: ptr(deploy1), Hostnames: []string{hostB}, ReadyDeployIDs: []string{deploy1}},
			// siteC exists only in the database: created, no release yet.
			{SiteID: "ccccc55555", Hostnames: []string{"ccccc55555.example.com"}},
		},
		Retained{SiteIDs: []string{siteKept}}
}

func TestReconcile(t *testing.T) {
	f, root := newTestFS(t)
	desired, keep := messyLayout(t, f, root)

	rep, err := f.Reconcile(context.Background(), desired, keep)
	if err != nil {
		t.Fatal(err)
	}
	want := ReconcileReport{
		RemovedTemp: []string{
			"by-host/" + tempLinkPrefix + "dead",
			"sites/" + siteA + "/" + tempLinkPrefix + "dead",
			"sites/" + siteA + "/releases/" + deploy2 + ".tmp",
			"sites/" + siteKept + "/releases/" + deploy2 + ".tmp",
		},
		RemovedReleases: []string{siteA + "/" + deploy3, siteA + "/junk.txt", siteB + "/" + deploy2},
		RemovedSites:    []string{"NOT-A-SITE", siteGone, "stray-file"},
		LinkedHosts:     []string{hostA, hostB, "ccccc55555.example.com", "promo.example.com"},
		UnlinkedHosts:   []string{"BAD..name", "dir.example.com", "gone.example.com", "junk"},
		FixedCurrent:    []string{siteA, siteB, "ccccc55555"},
		MissingCurrent:  []string{siteB},
	}
	slices.Sort(want.FixedCurrent)
	assertReport(t, rep, want)

	// Disk state.
	sites, err := os.ReadDir(filepath.Join(root, "sites"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range sites {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{siteA, siteB, "ccccc55555", siteKept}) {
		t.Fatalf("sites = %v", names)
	}
	if got := readFile(t, filepath.Join(root, "by-host", hostA, "index.html")); got != "a1" {
		t.Fatalf("hostA serves %q", got)
	}
	if got := readFile(t, filepath.Join(root, "by-host", "promo.example.com", "index.html")); got != "a1" {
		t.Fatalf("promo serves %q", got)
	}
	if !exists(t, filepath.Join(root, "sites", siteA, "releases", deploy2)) {
		t.Fatal("ready release removed")
	}
	if got := readlink(t, filepath.Join(root, "sites", siteB, "current")); got != emptyTarget {
		t.Fatalf("siteB current = %q", got)
	}
	// siteB had deploy2 on disk but not ready in the database: removed.
	if exists(t, filepath.Join(root, "sites", siteB, "releases", deploy2)) {
		t.Fatal("unknown release of siteB kept")
	}
	if got := readlink(t, filepath.Join(root, "sites", "ccccc55555", "current")); got != emptyTarget {
		t.Fatalf("siteC current = %q", got)
	}
	if fi, err := os.Stat(filepath.Join(root, "by-host", "ccccc55555.example.com")); err != nil || !fi.IsDir() {
		t.Fatalf("siteC host must resolve to a directory: %v", err)
	}
	// Retained site untouched apart from its staging dir.
	if got := readFile(t, filepath.Join(root, "sites", siteKept, "current", "index.html")); got != "k1" {
		t.Fatalf("kept site damaged: %q", got)
	}
	// BAD..name was an absolute link into the kept release: removing it must
	// not follow it (checked by the kept site read above).
	assertNoTempLinks(t, root)

	// Idempotent apart from the persistent MissingCurrent anomaly.
	rep, err = f.Reconcile(context.Background(), desired, keep)
	if err != nil {
		t.Fatal(err)
	}
	assertReport(t, rep, ReconcileReport{MissingCurrent: []string{siteB}})
}

func TestReconcileConsistentIsEmpty(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "a")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkHost(hostA, siteA); err != nil {
		t.Fatal(err)
	}
	if err := f.ActivateEmpty(siteB); err != nil {
		t.Fatal(err)
	}
	desired := []SiteLayout{
		{SiteID: siteA, CurrentDeployID: ptr(deploy1), Hostnames: []string{hostA}, ReadyDeployIDs: []string{deploy1}},
		{SiteID: siteB},
	}
	for range 2 {
		rep, err := f.Reconcile(context.Background(), desired, Retained{})
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Empty() {
			t.Fatalf("expected empty report, got %+v", rep)
		}
	}
	if got := readFile(t, filepath.Join(root, "by-host", hostA, "index.html")); got != "a" {
		t.Fatalf("served %q", got)
	}
}

func TestReconcileCurrentNotAlink(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "a")
	mkdir(t, filepath.Join(root, "sites", siteA, "current", "x"))
	// The current release is kept even if the database lost it from ready.
	rep, err := f.Reconcile(context.Background(), []SiteLayout{{SiteID: siteA, CurrentDeployID: ptr(deploy1)}}, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	assertReport(t, rep, ReconcileReport{FixedCurrent: []string{siteA}})
	if got := readFile(t, filepath.Join(root, "sites", siteA, "current", "index.html")); got != "a" {
		t.Fatalf("served %q", got)
	}
}

func TestReconcileRejectsInvalidInput(t *testing.T) {
	cases := map[string]struct {
		desired []SiteLayout
		keep    Retained
	}{
		"site id":        {desired: []SiteLayout{{SiteID: "../x"}}},
		"duplicate site": {desired: []SiteLayout{{SiteID: siteA}, {SiteID: siteA}}},
		"current id":     {desired: []SiteLayout{{SiteID: siteA, CurrentDeployID: ptr("..")}}},
		"ready id":       {desired: []SiteLayout{{SiteID: siteA, ReadyDeployIDs: []string{"a/b"}}}},
		"hostname":       {desired: []SiteLayout{{SiteID: siteA, Hostnames: []string{"../sites"}}}},
		"shared host":    {desired: []SiteLayout{{SiteID: siteA, Hostnames: []string{hostA}}, {SiteID: siteB, Hostnames: []string{hostA}}}},
		"kept id":        {keep: Retained{SiteIDs: []string{"A"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f, root := newTestFS(t)
			makeRelease(t, f, siteGone, deploy1, "g")
			if _, err := f.Reconcile(context.Background(), tc.desired, tc.keep); !errors.Is(err, ErrInvalidName) {
				t.Fatalf("want ErrInvalidName, got %v", err)
			}
			if !exists(t, filepath.Join(root, "sites", siteGone, "releases", deploy1)) {
				t.Fatal("disk touched despite invalid input")
			}
		})
	}
}

func TestReconcileCanceled(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteGone, deploy1, "g")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Reconcile(ctx, nil, Retained{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if !exists(t, filepath.Join(root, "sites", siteGone)) {
		t.Fatal("canceled reconcile removed a site")
	}
}

func TestReconcileInitializesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	f := NewFS(root)
	rep, err := f.Reconcile(context.Background(), []SiteLayout{{SiteID: siteA, Hostnames: []string{hostA}}}, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	assertReport(t, rep, ReconcileReport{LinkedHosts: []string{hostA}, FixedCurrent: []string{siteA}})
}

func TestReconcileBrokenRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "sites"))
	if _, err := NewFS(root).Reconcile(context.Background(), nil, Retained{}); err == nil {
		t.Fatal("expected error when sites is a file")
	}
}

func assertReport(t *testing.T, got, want ReconcileReport) {
	t.Helper()
	check := func(name string, g, w []string) {
		if !slices.Equal(g, w) {
			t.Errorf("%s = %v, want %v", name, g, w)
		}
	}
	check("RemovedTemp", got.RemovedTemp, want.RemovedTemp)
	check("RemovedReleases", got.RemovedReleases, want.RemovedReleases)
	check("RemovedSites", got.RemovedSites, want.RemovedSites)
	check("LinkedHosts", got.LinkedHosts, want.LinkedHosts)
	check("UnlinkedHosts", got.UnlinkedHosts, want.UnlinkedHosts)
	check("FixedCurrent", got.FixedCurrent, want.FixedCurrent)
	check("MissingCurrent", got.MissingCurrent, want.MissingCurrent)
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, p string) {
	t.Helper()
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}
