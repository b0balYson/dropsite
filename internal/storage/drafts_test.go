package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const draftA = "p-" + siteA + ".example.com"

func TestLinkDraft(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "v1")
	makeRelease(t, f, siteA, deploy2, "v2")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	// The host entry first: it gets an empty draft and serves nothing.
	if err := f.LinkDraftHost(draftA, siteA); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(root, "by-host", draftA)
	if got := readlink(t, host); got != "../sites/"+siteA+"/draft" || !HostTargetValid(got) {
		t.Fatalf("host target %q", got)
	}
	if exists(t, filepath.Join(host, "index.html")) {
		t.Fatal("no draft yet: the preview must be empty")
	}
	if err := f.LinkDraft(siteA, ptr(deploy2)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(host, "index.html")); got != "v2" {
		t.Fatalf("preview content %q", got)
	}
	if got := readFile(t, filepath.Join(root, "sites", siteA, "current", "index.html")); got != "v1" {
		t.Fatalf("current changed: %q", got)
	}
	if err := f.LinkDraft(siteA, nil); err != nil {
		t.Fatal(err)
	}
	if exists(t, filepath.Join(host, "index.html")) || !exists(t, host) {
		t.Fatal("a cleared draft keeps the host entry and serves nothing")
	}
}

func TestLinkDraftRejects(t *testing.T) {
	f, _ := newTestFS(t)
	if err := f.LinkDraft(siteA, ptr(deploy3)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing release: %v", err)
	}
	if err := f.LinkDraft("../x", nil); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("bad site: %v", err)
	}
	if err := f.LinkDraft(siteA, ptr("../current")); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("bad deploy: %v", err)
	}
	if err := f.LinkDraftHost("10.0.0.1", siteA); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("bad host: %v", err)
	}
}

func TestReconcileDrafts(t *testing.T) {
	ctx := context.Background()
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "v1")
	makeRelease(t, f, siteA, deploy2, "v2")
	makeRelease(t, f, siteB, deploy1, "b1")
	// siteB lost its draft host: its draft link must go.
	symlink(t, "releases/"+deploy1, filepath.Join(root, "sites", siteB, "draft"))
	desired := []SiteLayout{
		{SiteID: siteA, CurrentDeployID: ptr(deploy1), Hostnames: []string{hostA},
			ReadyDeployIDs: []string{deploy1, deploy2}, DraftHost: draftA, DraftDeployID: ptr(deploy2), Protected: true},
		{SiteID: siteB, CurrentDeployID: ptr(deploy1), Hostnames: []string{hostB}, ReadyDeployIDs: []string{deploy1}},
	}
	if _, err := f.Reconcile(ctx, desired, Retained{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "by-host", draftA, "index.html")); got != "v2" {
		t.Fatalf("draft preview %q", got)
	}
	if !exists(t, filepath.Join(root, "flags", string(FlagProtected), draftA)) {
		t.Fatal("the draft preview of a protected site must be protected")
	}
	if exists(t, filepath.Join(root, "sites", siteB, "draft")) {
		t.Fatal("stale draft link kept")
	}
	rep, err := f.Reconcile(ctx, desired, Retained{})
	if err != nil || !rep.Empty() {
		t.Fatalf("second reconcile changed %+v %v", rep, err)
	}

	// No draft now: the entry stays and serves the empty directory.
	desired[0].DraftDeployID = nil
	if _, err := f.Reconcile(ctx, desired, Retained{}); err != nil {
		t.Fatal(err)
	}
	if !exists(t, filepath.Join(root, "by-host", draftA)) || exists(t, filepath.Join(root, "by-host", draftA, "index.html")) {
		t.Fatal("cleared draft must keep an empty preview")
	}

	// A draft host colliding with a hostname is rejected.
	desired[1].DraftHost = hostA
	if _, err := f.Reconcile(ctx, desired, Retained{}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("collision: %v", err)
	}
	desired[1].DraftHost = ""
	desired[0].DraftDeployID = ptr(deploy3)
	if _, err := f.Reconcile(ctx, desired, Retained{}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("draft not ready: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "by-host", draftA)); err != nil {
		t.Fatalf("rejected input must not touch the disk: %v", err)
	}
}
