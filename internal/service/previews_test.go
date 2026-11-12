package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/storage"
)

func wildcard(c *config.Config) { c.TLSMode = config.TLSModeWildcard }

func previewHost(siteID, deployID string) string {
	return domain.PreviewLabel(siteID, deployID) + "." + testBase
}

func (e *env) hostExists(host string) bool {
	e.t.Helper()
	_, err := os.Lstat(filepath.Join(e.root, "by-host", host))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	return err == nil
}

func TestPreviewsFollowReleases(t *testing.T) {
	e := newEnv(t, wildcard)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "promo")
	id := site.Site.ID

	r1 := deploy(t, e, u, id, "v1")
	e.clock.Advance(time.Second)
	r2 := deploy(t, e, u, id, "v2")
	for d, want := range map[string]string{r1.Deploy.ID: "v1", r2.Deploy.ID: "v2"} {
		if got, err := e.served(previewHost(id, d), "index.html"); err != nil || got != want {
			t.Fatalf("preview of %s serves %q, %v", d, got, err)
		}
	}
	wantURL := "https://" + previewHost(id, r2.Deploy.ID) + "/"
	if got := e.svc.PreviewURL(r2.Deploy); got != wantURL {
		t.Fatalf("preview url %q", got)
	}
	if r2.Site.CurrentPreviewURL != wantURL {
		t.Fatalf("site view preview %q", r2.Site.CurrentPreviewURL)
	}

	// Rollback switches current, not previews.
	if _, err := e.svc.Rollback(ctx, u, id, r1.Deploy.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.served(previewHost(id, r2.Deploy.ID), "index.html"); got != "v2" {
		t.Fatalf("preview after rollback serves %q", got)
	}
	if got, _ := e.served("promo.pp.test", "index.html"); got != "v1" {
		t.Fatalf("site after rollback serves %q", got)
	}

	// Deleting the site removes every preview at once.
	if err := e.svc.DeleteSite(ctx, u, id); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{r1.Deploy.ID, r2.Deploy.ID} {
		if e.hostExists(previewHost(id, d)) {
			t.Fatalf("preview of %s survived deletion", d)
		}
	}
}

func TestPrunedReleaseLosesPreview(t *testing.T) {
	e := newEnv(t, wildcard)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	id := site.Site.ID
	var ds []domain.Deploy
	for i := range 6 {
		ds = append(ds, deploy(t, e, u, id, fmt.Sprintf("v%d", i+1)).Deploy)
		e.clock.Advance(time.Second)
	}
	if e.hostExists(previewHost(id, ds[0].ID)) {
		t.Fatal("pruned release must lose its preview")
	}
	if !e.hostExists(previewHost(id, ds[1].ID)) {
		t.Fatal("kept release must keep its preview")
	}
	pruned, err := e.repo.GetDeploy(ctx, id, ds[0].ID)
	if err != nil || pruned.Status != domain.DeployPruned {
		t.Fatalf("deploy %+v %v", pruned, err)
	}
	if got := e.svc.PreviewURL(pruned); got != "" {
		t.Fatalf("pruned release has preview url %q", got)
	}
}

func TestNoPreviewsInOnDemandMode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	r := deploy(t, e, u, site.Site.ID, "v1")
	if e.hostExists(previewHost(site.Site.ID, r.Deploy.ID)) {
		t.Fatal("ondemand mode must not create previews")
	}
	if e.svc.PreviewURL(r.Deploy) != "" || r.Site.CurrentPreviewURL != "" {
		t.Fatal("ondemand mode must not report preview urls")
	}
}

func TestPreviewURLUsesSitesPort(t *testing.T) {
	e := newEnv(t, wildcard, func(c *config.Config) { c.SitesHTTPSPort = 18443 })
	d := domain.Deploy{ID: "20261112-101530-x7k2m9", SiteID: "k3f9x2m7qa", Status: domain.DeployReady}
	if got := e.svc.PreviewURL(d); got != "https://p-20261112-101530-x7k2m9-k3f9x2m7qa.pp.test:18443/" {
		t.Fatalf("url %q", got)
	}
}

// restart builds a new service over the same database and volume with the
// given TLS mode and runs its start-up reconciliation.
func (e *env) restart(mode string) *Service {
	e.t.Helper()
	cfg := *e.svc.Config()
	cfg.TLSMode = mode
	svc := New(Deps{Config: &cfg, Repo: e.repo, Files: storage.NewFS(cfg.SitesDir), Now: e.clock.Now, UploadDir: cfg.StateDir})
	if err := svc.Start(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return svc
}

func TestReconcileFollowsTLSMode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	r := deploy(t, e, u, site.Site.ID, "v1")
	host := previewHost(site.Site.ID, r.Deploy.ID)

	e.restart(config.TLSModeWildcard)
	if got, err := e.served(host, "index.html"); err != nil || got != "v1" {
		t.Fatalf("switching to wildcard must create previews: %q %v", got, err)
	}
	e.restart(config.TLSModeOnDemand)
	if e.hostExists(host) {
		t.Fatal("switching back to ondemand must remove previews")
	}
}

// A slug of preview form taken before the reservation existed wins over the
// preview: it is neither overwritten nor removed.
func TestLegacySlugShadowsPreview(t *testing.T) {
	e := newEnv(t, wildcard)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	a, _ := e.svc.CreateSite(ctx, u, "a", "")
	b, _ := e.svc.CreateSite(ctx, u, "b", "")
	ra := deploy(t, e, u, a.Site.ID, "site a")
	deploy(t, e, u, b.Site.ID, "site b")
	host := previewHost(a.Site.ID, ra.Deploy.ID)
	if _, err := e.repo.SetSlugHost(ctx, b.Site.ID,
		&domain.Hostname{Hostname: host, SiteID: b.Site.ID, Kind: domain.HostSlug, Verified: true, CreatedAt: e.clock.Now()},
		e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.files.LinkHost(host, b.Site.ID); err != nil {
		t.Fatal(err)
	}

	e.restart(config.TLSModeWildcard)
	if got, _ := e.served(host, "index.html"); got != "site b" {
		t.Fatalf("slug must win over the preview, served %q", got)
	}
	// Pruning site A's release must not remove site B's slug.
	for i := range 6 {
		deploy(t, e, u, a.Site.ID, fmt.Sprintf("a%d", i))
		e.clock.Advance(time.Second)
	}
	if got, _ := e.served(host, "index.html"); got != "site b" {
		t.Fatalf("prune removed a slug, served %q", got)
	}
}
