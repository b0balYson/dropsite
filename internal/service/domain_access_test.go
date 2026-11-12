package service

import (
	"context"
	"testing"
)

// TestExternalDomainsCarrySiteFlags: an approved external domain is one more
// address of the site and gets the same markers; a pending one gets none.
func TestExternalDomainsCarrySiteFlags(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "Docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Noindex: bptr(true), Password: sptr("visitor-pass-1")}); err != nil {
		t.Fatal(err)
	}
	const ext = "promo.example.ru"
	if _, err := e.svc.AddDomain(ctx, alice, v.Site.ID, ext); err != nil {
		t.Fatal(err)
	}
	if e.marker("protected", ext) {
		t.Fatal("pending domain marked")
	}
	if d, _ := e.svc.CheckVisitorAccess(ctx, ext, ""); d != AccessUnknown {
		t.Fatalf("pending domain is an address: %v", d)
	}

	if _, err := e.svc.ApproveDomain(ctx, e.admin, ext); err != nil {
		t.Fatal(err)
	}
	if !e.marker("protected", ext) || !e.marker("noindex", ext) {
		t.Fatal("approved domain not marked")
	}
	if d, _ := e.svc.CheckVisitorAccess(ctx, ext, ""); d != AccessLogin {
		t.Fatalf("approved domain without cookie: %v", d)
	}
	// Removing protection reaches the domain; so does a rejection.
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Noindex: bptr(false)}); err != nil {
		t.Fatal(err)
	}
	if e.marker("noindex", ext) {
		t.Fatal("noindex marker kept on the domain")
	}
	if _, err := e.svc.RejectDomain(ctx, e.admin, ext, "not ours"); err != nil {
		t.Fatal(err)
	}
	if e.marker("protected", ext) {
		t.Fatal("rejected domain kept its marker")
	}
	// Reconciliation agrees: nothing to change.
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if e.marker("protected", ext) {
		t.Fatal("reconcile marked a rejected domain")
	}
}
