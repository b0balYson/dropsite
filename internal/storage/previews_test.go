package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const previewA1 = "p-" + deploy1 + "-" + siteA + ".example.com"

func TestLinkPreview(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "v1")
	makeRelease(t, f, siteA, deploy2, "v2")
	if err := f.ActivateRelease(siteA, deploy2); err != nil {
		t.Fatal(err)
	}
	if err := f.LinkPreview(previewA1, siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "by-host", previewA1)
	if got := readlink(t, link); got != "../sites/"+siteA+"/releases/"+deploy1 {
		t.Fatalf("target %q", got)
	}
	// The preview serves its own release, not current.
	if got := readFile(t, filepath.Join(link, "index.html")); got != "v1" {
		t.Fatalf("preview content %q", got)
	}
	// Relinking is atomic and idempotent.
	if err := f.LinkPreview(previewA1, siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	if err := f.UnlinkHost(previewA1); err != nil {
		t.Fatal(err)
	}
	if exists(t, link) {
		t.Fatal("preview link must be removed")
	}
}

func TestLinkPreviewRejects(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "v1")
	if err := f.LinkPreview(previewA1, siteA, deploy3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release: %v", err)
	}
	for _, tc := range [][3]string{
		{"../x", siteA, deploy1},
		{"10.0.0.1", siteA, deploy1},
		{previewA1, "../../etc", deploy1},
		{previewA1, siteA, "../current"},
	} {
		if err := f.LinkPreview(tc[0], tc[1], tc[2]); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%v: want ErrInvalidName, got %v", tc, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "by-host"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("nothing may be linked: %v %v", entries, err)
	}
}

func TestReconcilePreviews(t *testing.T) {
	ctx := context.Background()
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "v1")
	makeRelease(t, f, siteA, deploy2, "v2")
	stale := "p-" + deploy3 + "-" + siteA + ".example.com"
	previewA2 := "p-" + deploy2 + "-" + siteA + ".example.com"
	// A stale preview, and a preview pointing at the wrong release.
	symlink(t, "../sites/"+siteA+"/releases/"+deploy1, filepath.Join(root, "by-host", stale))
	symlink(t, "../sites/"+siteA+"/releases/"+deploy1, filepath.Join(root, "by-host", previewA2))
	missing := "p-" + deploy3 + "-" + siteA + ".example.org"

	desired := []SiteLayout{{
		SiteID: siteA, CurrentDeployID: ptr(deploy2), Hostnames: []string{hostA},
		ReadyDeployIDs: []string{deploy1, deploy2, deploy3},
		Previews: []Preview{
			{Hostname: previewA1, DeployID: deploy1},
			{Hostname: previewA2, DeployID: deploy2},
			// Its release directory is gone: no dangling link is created.
			{Hostname: missing, DeployID: deploy3},
		},
	}}
	rep, err := f.Reconcile(ctx, desired, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(root, "by-host", previewA1)); got != previewTarget(siteA, deploy1) {
		t.Fatalf("preview a1 -> %q", got)
	}
	if got := readFile(t, filepath.Join(root, "by-host", previewA2, "index.html")); got != "v2" {
		t.Fatalf("preview a2 serves %q", got)
	}
	if exists(t, filepath.Join(root, "by-host", stale)) || exists(t, filepath.Join(root, "by-host", missing)) {
		t.Fatal("stale and dangling previews must not exist")
	}
	if !slices.Contains(rep.LinkedHosts, previewA1) || !slices.Contains(rep.LinkedHosts, previewA2) ||
		!slices.Contains(rep.UnlinkedHosts, stale) {
		t.Fatalf("report %+v", rep)
	}
	if !slices.Contains(rep.MissingPreviews, missing) {
		t.Fatalf("missing preview must be reported: %+v", rep)
	}
	// Idempotent apart from the persistent anomaly.
	rep, err = f.Reconcile(ctx, desired, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.LinkedHosts)+len(rep.UnlinkedHosts) != 0 || len(rep.MissingPreviews) != 1 {
		t.Fatalf("second run: %+v", rep)
	}

	// Without previews in the desired state (ondemand mode) all are removed.
	desired[0].Previews = nil
	if _, err := f.Reconcile(ctx, desired, Retained{}); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(root, "by-host", previewA1)) || exists(t, filepath.Join(root, "by-host", previewA2)) {
		t.Fatal("previews must be removed")
	}
	if !exists(t, filepath.Join(root, "by-host", hostA)) {
		t.Fatal("ordinary host must stay")
	}
}

func TestReconcilePreviewConflicts(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "v1")
	cases := map[string][]SiteLayout{
		"preview equals hostname": {{SiteID: siteA, Hostnames: []string{previewA1}, ReadyDeployIDs: []string{deploy1},
			Previews: []Preview{{Hostname: previewA1, DeployID: deploy1}}}},
		"preview of unknown release": {{SiteID: siteA, ReadyDeployIDs: []string{deploy1},
			Previews: []Preview{{Hostname: previewA1, DeployID: deploy2}}}},
		"invalid preview name": {{SiteID: siteA, ReadyDeployIDs: []string{deploy1},
			Previews: []Preview{{Hostname: "../x", DeployID: deploy1}}}},
	}
	for name, desired := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := f.Reconcile(context.Background(), desired, Retained{}); !errors.Is(err, ErrInvalidName) {
				t.Fatalf("want ErrInvalidName, got %v", err)
			}
			if exists(t, filepath.Join(root, "by-host", previewA1)) {
				t.Fatal("nothing may change on rejected input")
			}
		})
	}
}
