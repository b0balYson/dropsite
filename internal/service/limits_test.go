package service

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

func i64(v int64) *int64 { return &v }

func TestLimitPrecedence(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, func(c *config.Config) { c.Limits.MaxFiles = 1000 })
	alice, bob := e.user("alice@pp.test"), e.user("bob@pp.test")

	// Environment only.
	_, lim, _, err := e.svc.Me(ctx, alice)
	if err != nil || lim.MaxFiles != 1000 {
		t.Fatalf("env: %d %v", lim.MaxFiles, err)
	}
	// Admin setting beats the environment.
	if _, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{MaxFiles: i64(1500)}); err != nil {
		t.Fatal(err)
	}
	// User override beats the admin setting, only for that user.
	if _, err := e.svc.SetUserLimits(ctx, e.admin, alice.User.ID, domain.LimitOverrides{MaxFiles: i64(3000)}); err != nil {
		t.Fatal(err)
	}
	_, la, _, _ := e.svc.Me(ctx, alice)
	_, lb, _, _ := e.svc.Me(ctx, bob)
	if la.MaxFiles != 3000 || lb.MaxFiles != 1500 {
		t.Fatalf("alice %d bob %d", la.MaxFiles, lb.MaxFiles)
	}
	// Reset falls back to the admin setting.
	ul, err := e.svc.SetUserLimits(ctx, e.admin, alice.User.ID, domain.LimitOverrides{})
	if err != nil || ul.Effective.MaxFiles != 1500 || !ul.Overrides.Empty() {
		t.Fatalf("reset: %+v %v", ul, err)
	}
}

func TestUserLimitsAdminOnlyAndValidated(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")

	_, err := e.svc.GetUserLimits(ctx, alice, alice.User.ID)
	wantNotFound(t, err)
	_, err = e.svc.SetUserLimits(ctx, alice, alice.User.ID, domain.LimitOverrides{MaxFiles: i64(5)})
	wantNotFound(t, err)
	_, err = e.svc.SetUserLimits(ctx, e.admin, 9999, domain.LimitOverrides{MaxFiles: i64(5)})
	wantNotFound(t, err)

	for name, o := range map[string]domain.LimitOverrides{
		"keep below 2":        {KeepReleases: i64(1)},
		"zero files":          {MaxFiles: i64(0)},
		"file above own site": {MaxSiteBytes: i64(10 << 20), MaxFileBytes: i64(11 << 20)},
		"file above global":   {MaxFileBytes: i64(e.svc.cfg.Limits.MaxSiteBytes + 1)},
	} {
		_, err := e.svc.SetUserLimits(ctx, e.admin, alice.User.ID, o)
		wantCode(t, err, domain.CodeSettingInvalid)
		if got, _ := e.svc.GetUserLimits(ctx, e.admin, alice.User.ID); !got.Overrides.Empty() {
			t.Fatalf("%s: stored %+v", name, got.Overrides)
		}
	}

	ul, err := e.svc.SetUserLimits(ctx, e.admin, alice.User.ID, domain.LimitOverrides{MaxSiteBytes: i64(100 << 20)})
	if err != nil || ul.Effective.MaxSiteBytes != 100<<20 || ul.Global.MaxSiteBytes == 100<<20 {
		t.Fatalf("set: %+v %v", ul, err)
	}
	rows, _, err := e.svc.ListAudit(ctx, e.admin, AuditFilter{Action: "user.limits"})
	if err != nil || len(rows) != 1 || rows[0].Meta["limits"] == nil {
		t.Fatalf("audit %+v %v", rows, err)
	}
}

func TestPersonalLimitsApplyToSitesAndDeploys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, func(c *config.Config) {
		c.Limits.MaxSitesPerUser = 1
		c.Limits.MaxSiteBytes = 1000
		c.Limits.MaxFileBytes = 1000
	})
	alice := e.user("alice@pp.test")
	v, err := e.svc.CreateSite(ctx, alice, "One", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateSite(ctx, alice, "Two", "")
	wantCode(t, err, domain.CodeSiteLimit)
	if _, err := e.svc.SetUserLimits(ctx, e.admin, alice.User.ID, domain.LimitOverrides{
		MaxSitesPerUser: i64(2), MaxSiteBytes: i64(100_000), MaxFileBytes: i64(100_000),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSite(ctx, alice, "Two", ""); err != nil {
		t.Fatal(err)
	}

	big := map[string]string{"index.html": strings.Repeat("x", 5000)}
	// The admin publishes to alice's site: alice's (owner's) limits apply.
	if _, err := e.svc.Deploy(ctx, e.admin, v.Site.ID, zipOf(t, big), domain.SourceAPI); err != nil {
		t.Fatalf("deploy under owner limit: %v", err)
	}
	// The admin's own site follows the global 1000 bytes.
	own, err := e.svc.CreateSite(ctx, e.admin, "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Deploy(ctx, e.admin, own.Site.ID, zipOf(t, big), domain.SourceAPI)
	if de := domain.AsError(err); de == nil || de.Kind != domain.KindTooLarge {
		t.Fatalf("want too large, got %v", err)
	}
	lim, err := e.svc.UploadLimit(ctx, alice, v.Site.ID)
	if err != nil || lim <= 100_000 {
		t.Fatalf("upload limit %d %v", lim, err)
	}
	_, err = e.svc.UploadLimit(ctx, alice, own.Site.ID)
	wantNotFound(t, err)
}

func TestPersonalKeepReleases(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	if _, err := e.svc.SetUserLimits(ctx, e.admin, alice.User.ID, domain.LimitOverrides{KeepReleases: i64(2)}); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.CreateSite(ctx, alice, "Site", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		e.clock.Advance(1e9)
		if _, err := e.svc.Deploy(ctx, alice, v.Site.ID, zipOf(t, map[string]string{"index.html": string(rune('a' + i))}), domain.SourceAPI); err != nil {
			t.Fatal(err)
		}
	}
	ds, _, err := e.svc.ListDeploys(ctx, alice, v.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	ready := 0
	for _, d := range ds {
		if d.Status == domain.DeployReady {
			ready++
		}
	}
	if ready != 2 {
		t.Fatalf("ready releases %d, want 2", ready)
	}
}
