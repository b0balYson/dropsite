package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// External domains are ordinary by-host entries: any lower-case DNS name,
// including deep names, punycode and the apex of a foreign domain.
func TestExternalHostLinks(t *testing.T) {
	f, root := newTestFS(t)
	makeRelease(t, f, siteA, deploy1, "a")
	if err := f.ActivateRelease(siteA, deploy1); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"promo.example.ru", "example.ru", "a.b.c.example.org",
		"xn--l1adbbf.xn--e1afmkfd.xn--p1ai", "promo.external.localhost",
	}
	for _, h := range names {
		if err := f.LinkHost(h, siteA); err != nil {
			t.Fatalf("%s: %v", h, err)
		}
		if got := readFile(t, filepath.Join(root, "by-host", h, "index.html")); got != "a" {
			t.Fatalf("%s served %q", h, got)
		}
	}
	for _, bad := range []string{"Promo.example.ru", "promo.example.ru.", "203.0.113.7", "x/../y.ru", "*.example.ru"} {
		if err := f.LinkHost(bad, siteA); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("%s: want ErrInvalidName, got %v", bad, err)
		}
	}
	for _, h := range names {
		if err := f.UnlinkHost(h); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(root, "by-host", h)); !os.IsNotExist(err) {
			t.Fatalf("%s still linked: %v", h, err)
		}
	}
	// Unlinking a never-approved domain is a no-op.
	if err := f.UnlinkHost("pending.example.ru"); err != nil {
		t.Fatal(err)
	}
}

// A stale link of a domain that is no longer approved is removed by reconcile.
func TestReconcileRemovesUnapprovedExternalHost(t *testing.T) {
	f, root := newTestFS(t)
	if err := f.ActivateEmpty(siteA); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{hostA, "approved.example.ru", "rejected.example.ru"} {
		if err := f.LinkHost(h, siteA); err != nil {
			t.Fatal(err)
		}
	}
	desired := []SiteLayout{{SiteID: siteA, Hostnames: []string{hostA, "approved.example.ru"}}}
	rep, err := f.Reconcile(context.Background(), desired, Retained{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.UnlinkedHosts, []string{"rejected.example.ru"}) || len(rep.LinkedHosts) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if _, err := os.Lstat(filepath.Join(root, "by-host", "approved.example.ru")); err != nil {
		t.Fatal(err)
	}
}
