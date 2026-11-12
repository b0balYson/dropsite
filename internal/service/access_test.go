package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func bptr(v bool) *bool           { return &v }
func sptr(v string) *string       { return &v }
func tptr(v time.Time) *time.Time { return &v }

func (e *env) marker(flag, host string) bool {
	e.t.Helper()
	_, err := os.Lstat(filepath.Join(e.root, "flags", flag, host))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	return err == nil
}

func (e *env) auditActions(target string) []string {
	e.t.Helper()
	rows, _, err := e.svc.ListAudit(context.Background(), e.admin, AuditFilter{})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Target == target {
			out = append(out, rows[i].Action)
		}
	}
	return out
}

func TestNoindexFlagFollowsAddresses(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "Promo", "old-name")
	if err != nil {
		t.Fatal(err)
	}
	def := v.Site.ID + "." + testBase
	v, err = e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Noindex: bptr(true)})
	if err != nil || !v.Site.Noindex {
		t.Fatalf("noindex: %+v %v", v.Site, err)
	}
	if !e.marker("noindex", def) || !e.marker("noindex", "old-name."+testBase) {
		t.Fatal("noindex markers missing")
	}
	// Changing the slug moves the marker.
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Slug: sptr("new-name")}); err != nil {
		t.Fatal(err)
	}
	if e.marker("noindex", "old-name."+testBase) || !e.marker("noindex", "new-name."+testBase) {
		t.Fatal("marker did not follow the slug")
	}
	// Apex inherits the flag; clearing apex removes it.
	if _, err := e.svc.SetApex(ctx, e.admin, v.Site.ID); err != nil {
		t.Fatal(err)
	}
	if !e.marker("noindex", testBase) {
		t.Fatal("apex marker missing")
	}
	if _, err := e.svc.ClearApex(ctx, e.admin, v.Site.ID); err != nil {
		t.Fatal(err)
	}
	if e.marker("noindex", testBase) {
		t.Fatal("apex marker kept")
	}
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Noindex: bptr(false)}); err != nil {
		t.Fatal(err)
	}
	if e.marker("noindex", def) || e.marker("noindex", "new-name."+testBase) {
		t.Fatal("markers kept after disabling")
	}
	got := e.auditActions(siteTarget(v.Site.ID))
	if got[len(got)-1] != "site.noindex" {
		t.Fatalf("audit %v", got)
	}
}

func TestProtectionLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "Docs", "docs")
	if err != nil {
		t.Fatal(err)
	}
	id, def := v.Site.ID, v.Site.ID+"."+testBase

	_, err = e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Protected: bptr(true)})
	wantCode(t, err, domain.CodeSitePasswordRequired)
	_, err = e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Password: sptr("short")})
	wantCode(t, err, domain.CodePasswordShort)
	_, err = e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Protected: bptr(false), Password: sptr("long-enough-pass")})
	wantCode(t, err, domain.CodeInvalidInput)
	// A rejected request changes nothing, not even other fields.
	_, err = e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Title: sptr("Renamed"), Password: sptr("short")})
	wantCode(t, err, domain.CodePasswordShort)
	if got, _ := e.svc.GetSite(ctx, alice, id); got.Site.Title != "Docs" || got.Site.Protected {
		t.Fatalf("partial update: %+v", got.Site)
	}

	v, err = e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Protected: bptr(true), Password: sptr("first-password")})
	if err != nil || !v.Site.Protected {
		t.Fatalf("protect: %+v %v", v.Site, err)
	}
	if !e.marker("protected", def) || !e.marker("protected", "docs."+testBase) {
		t.Fatal("protected markers missing")
	}
	acc, _ := e.repo.GetSiteAccess(ctx, id)
	if acc.PasswordHash == "" || acc.PasswordHash == "first-password" {
		t.Fatalf("hash %q", acc.PasswordHash)
	}
	// Protected: true again without a password keeps the current one.
	if _, err := e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Protected: bptr(true)}); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.repo.GetSiteAccess(ctx, id); again != acc {
		t.Fatal("password changed without a new one")
	}
	if _, err := e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Password: sptr("second-password")}); err != nil {
		t.Fatal(err)
	}
	v, err = e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Protected: bptr(false)})
	if err != nil || v.Site.Protected || e.marker("protected", def) {
		t.Fatalf("unprotect: %+v %v", v.Site, err)
	}
	got := e.auditActions(siteTarget(id))
	want := []string{"site.create", "site.protect", "site.password_change", "site.unprotect"}
	if len(got) != len(want) {
		t.Fatalf("audit %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("audit %v", got)
		}
	}
}

func TestAccessChangesRespectOwnership(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob := e.user("alice@pp.test"), e.user("bob@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "Mine", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []SiteUpdate{
		{Noindex: bptr(true)},
		{Password: sptr("long-enough-pass")},
		{Expiry: &Expiry{At: tptr(e.clock.Now().Add(time.Hour))}},
	} {
		_, err := e.svc.UpdateSite(ctx, bob, v.Site.ID, u)
		wantNotFound(t, err)
	}
	if e.marker("noindex", v.Site.ID+"."+testBase) || e.marker("protected", v.Site.ID+"."+testBase) {
		t.Fatal("foreign user set markers")
	}
	// The admin may change any site.
	if _, err := e.svc.UpdateSite(ctx, e.admin, v.Site.ID, SiteUpdate{Noindex: bptr(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteClearsMarkersAndReconcileRestoresThem(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	keep, _ := e.svc.CreateSite(ctx, alice, "Keep", "keep")
	gone, _ := e.svc.CreateSite(ctx, alice, "Gone", "")
	for _, id := range []string{keep.Site.ID, gone.Site.ID} {
		if _, err := e.svc.UpdateSite(ctx, alice, id, SiteUpdate{Noindex: bptr(true), Password: sptr("long-enough-pass")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.DeleteSite(ctx, alice, gone.Site.ID); err != nil {
		t.Fatal(err)
	}
	if e.marker("noindex", gone.Site.ID+"."+testBase) || e.marker("protected", gone.Site.ID+"."+testBase) {
		t.Fatal("deleted site kept markers")
	}
	// Lose a marker and add a stray one; a restart repairs both.
	if err := os.Remove(filepath.Join(e.root, "flags", "protected", "keep."+testBase)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "flags", "noindex", "stray."+testBase), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.marker("protected", "keep."+testBase) || e.marker("noindex", "stray."+testBase) {
		t.Fatal("reconcile did not repair markers")
	}
}

func TestExpiryValidation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, _ := e.svc.CreateSite(ctx, alice, "Promo", "")
	now := e.clock.Now()
	for _, at := range []time.Time{now, now.Add(-time.Second), now.Add(MaxExpiry + time.Hour)} {
		_, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{At: tptr(at)}})
		wantCode(t, err, domain.CodeExpiryInvalid)
	}
	at := now.Add(72 * time.Hour)
	v, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{At: &at}})
	if err != nil || v.Site.ExpiresAt == nil || !v.Site.ExpiresAt.Equal(at) {
		t.Fatalf("set expiry: %+v %v", v.Site.ExpiresAt, err)
	}
	v, err = e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{}})
	if err != nil || v.Site.ExpiresAt != nil {
		t.Fatalf("clear expiry: %+v %v", v.Site.ExpiresAt, err)
	}
}

func TestSiteExpires(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, _ := e.svc.CreateSite(ctx, alice, "Promo", "promo-may")
	other, _ := e.svc.CreateSite(ctx, alice, "Other", "")
	if _, err := e.svc.Deploy(ctx, alice, v.Site.ID, zipOf(t, map[string]string{"index.html": "hi"}), domain.SourceAPI); err != nil {
		t.Fatal(err)
	}
	at := e.clock.Now().Add(time.Hour)
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{At: &at}, Noindex: bptr(true)}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ExpireSites(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := e.served("promo-may."+testBase, "index.html"); err != nil || got != "hi" {
		t.Fatalf("served before expiry: %q %v", got, err)
	}
	e.clock.Advance(time.Hour)
	if err := e.svc.ExpireSites(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.served("promo-may."+testBase, "index.html"); err == nil {
		t.Fatal("expired site still served")
	}
	if e.marker("noindex", "promo-may."+testBase) {
		t.Fatal("expired site kept markers")
	}
	_, err := e.svc.GetSite(ctx, alice, v.Site.ID)
	wantNotFound(t, err)
	if _, err := e.svc.GetSite(ctx, alice, other.Site.ID); err != nil {
		t.Fatalf("other site affected: %v", err)
	}
	// The slug is free again at once.
	if _, err := e.svc.UpdateSite(ctx, alice, other.Site.ID, SiteUpdate{Slug: sptr("promo-may")}); err != nil {
		t.Fatal(err)
	}
	if got := e.auditActions(siteTarget(v.Site.ID)); got[len(got)-1] != "site.expire" {
		t.Fatalf("audit %v", got)
	}
	// Purged by the usual soft-delete rule.
	e.clock.Advance(DeletedSiteTTL + time.Minute)
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "sites", v.Site.ID)); !os.IsNotExist(err) {
		t.Fatalf("files not purged: %v", err)
	}
}

func TestStartExpiresBeforeReconcile(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v, _ := e.svc.CreateSite(ctx, alice, "Promo", "")
	at := e.clock.Now().Add(time.Minute)
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Expiry: &Expiry{At: &at}}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour)
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.root, "by-host", v.Site.ID+"."+testBase)); !os.IsNotExist(err) {
		t.Fatalf("expired site linked after start: %v", err)
	}
}

func TestRunExpiryStops(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.RunExpiry(ctx, time.Millisecond); close(done) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done
}
