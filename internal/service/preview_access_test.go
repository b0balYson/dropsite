package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// TestPreviewsCarrySiteFlags: in wildcard mode every release has its own
// preview name, which must be as restricted as the site's other addresses.
func TestPreviewsCarrySiteFlags(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, wildcard)
	alice := e.user("alice@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "Docs", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.svc.Deploy(ctx, alice, v.Site.ID, zipOf(t, map[string]string{"index.html": "1"}), domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	p1 := e.svc.previewHost(v.Site.ID, first.Deploy.ID)

	// Turning flags on marks the existing preview.
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Noindex: bptr(true), Password: sptr("visitor-pass-1")}); err != nil {
		t.Fatal(err)
	}
	if !e.marker("protected", p1) || !e.marker("noindex", p1) {
		t.Fatal("existing preview not marked")
	}
	// A new release gets a marked preview from the start.
	e.clock.Advance(1e9)
	second, err := e.svc.Deploy(ctx, alice, v.Site.ID, zipOf(t, map[string]string{"index.html": "2"}), domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	p2 := e.svc.previewHost(v.Site.ID, second.Deploy.ID)
	if !e.marker("protected", p2) || !e.marker("noindex", p2) {
		t.Fatal("new preview not marked")
	}
	// The visitor check knows preview names: login is required there too,
	// and a cookie for the site opens them.
	d, err := e.svc.CheckVisitorAccess(ctx, p2, "")
	if err != nil || d != AccessLogin {
		t.Fatalf("preview without cookie: %v %v", d, err)
	}
	c, err := e.visitorLogin(p2, "visitor-pass-1", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.CheckVisitorAccess(ctx, p1, c.Value); d != AccessAllow {
		t.Fatalf("site cookie on preview: %v", d)
	}
	// Unknown preview-shaped names stay unknown.
	fake := domain.PreviewLabel(v.Site.ID, "20990101-000000-zzzzzz") + "." + testBase
	if d, _ := e.svc.CheckVisitorAccess(ctx, fake, ""); d != AccessUnknown {
		t.Fatalf("fake preview: %v", d)
	}

	// A lost marker is restored by reconciliation, a stray one removed.
	if err := os.Remove(filepath.Join(e.root, "flags", "protected", p1)); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.marker("protected", p1) {
		t.Fatal("reconcile did not restore the preview marker")
	}

	// Removing protection clears the previews too; deletion clears the rest.
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Protected: bptr(false)}); err != nil {
		t.Fatal(err)
	}
	if e.marker("protected", p1) || e.marker("protected", p2) {
		t.Fatal("preview markers kept after unprotect")
	}
	if err := e.svc.DeleteSite(ctx, alice, v.Site.ID); err != nil {
		t.Fatal(err)
	}
	if e.marker("noindex", p1) || e.marker("noindex", p2) {
		t.Fatal("preview markers kept after delete")
	}
}
