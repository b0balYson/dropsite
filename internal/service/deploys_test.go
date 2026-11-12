package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

func deploy(t *testing.T, e *env, a Actor, ref, body string) DeployResult {
	t.Helper()
	res, err := e.svc.Deploy(context.Background(), a, ref, zipOf(t, map[string]string{"index.html": body, "common.txt": "same"}), domain.SourceCLI)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDeployResult(t *testing.T) {
	e := newEnv(t)
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(context.Background(), u, "p", "promo")
	res := deploy(t, e, u, "promo", "v1")
	d := res.Deploy
	if d.Status != domain.DeployReady || d.Source != domain.SourceCLI || d.FileCount != 2 || d.SizeBytes != 6 || d.CreatedBy != u.User.ID {
		t.Fatalf("deploy %+v", d)
	}
	if res.Site.URL != "https://promo.pp.test/" || len(res.Site.URLs) != 2 || res.Site.Current == nil || res.Site.Current.ID != d.ID {
		t.Fatalf("result %+v", res.Site)
	}
	if body, _ := e.served(site.Site.ID+".pp.test", "index.html"); body != "v1" {
		t.Fatal("not served")
	}
}

func TestFailedDeployKeepsCurrentRelease(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	deploy(t, e, u, site.Site.ID, "v1")
	bad := map[string]*bytes.Reader{
		"no index":  zipOf(t, map[string]string{"about.html": "x"}),
		"traversal": zipOf(t, map[string]string{"index.html": "x", "../../evil": "x"}),
		"garbage":   bytes.NewReader([]byte("this is not an archive")),
	}
	for name, r := range bad {
		if _, err := e.svc.Deploy(ctx, u, site.Site.ID, r, domain.SourceUI); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
		if body, _ := e.served(site.Site.ID+".pp.test", "index.html"); body != "v1" {
			t.Fatalf("%s: current release changed", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(e.root, "sites", site.Site.ID, "releases"))
	if len(entries) != 1 {
		t.Fatalf("leftovers in releases: %d entries", len(entries))
	}
	if ds, _, _ := e.svc.ListDeploys(ctx, u, site.Site.ID); len(ds) != 1 {
		t.Fatal("failed deploys must not appear in history")
	}
	uploads, _ := filepath.Glob(filepath.Join(e.svc.uploadDir, "upload-*"))
	if len(uploads) != 0 {
		t.Fatal("temporary uploads must be removed")
	}
}

func TestUploadSizeIsCutOff(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.Limits.MaxSiteBytes = 1000
		c.Limits.MaxFileBytes = 1000
		c.Limits.MaxFiles = 1
	})
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	huge := bytes.NewReader(make([]byte, 3<<20))
	_, err := e.svc.Deploy(ctx, u, site.Site.ID, huge, domain.SourceUI)
	wantCode(t, err, domain.CodeArchiveTooLarge)
	if huge.Len() == 0 {
		t.Fatal("reading must stop shortly after the limit")
	}
}

func TestRollbackAndHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	ref := site.Site.ID
	host := ref + ".pp.test"
	if _, err := e.svc.Rollback(ctx, u, ref, ""); err == nil {
		t.Fatal("nothing to roll back to")
	}
	v1 := deploy(t, e, u, ref, "v1").Deploy
	e.clock.Advance(time.Second)
	if _, err := e.svc.Rollback(ctx, u, ref, ""); err == nil {
		t.Fatal("single release has no previous")
	} else {
		wantCode(t, err, domain.CodeNoPrevious)
	}
	v2 := deploy(t, e, u, ref, "v2").Deploy
	e.clock.Advance(time.Second)
	res, err := e.svc.Rollback(ctx, u, ref, "")
	if err != nil || res.Deploy.ID != v1.ID {
		t.Fatalf("rollback to previous: %v", err)
	}
	if body, _ := e.served(host, "index.html"); body != "v1" {
		t.Fatal("must serve v1")
	}
	ds, _, _ := e.svc.ListDeploys(ctx, u, ref)
	if len(ds) != 2 || ds[0].ID != v2.ID {
		t.Fatal("rollback must not create a release; history newest first")
	}
	if _, err := e.svc.Rollback(ctx, u, ref, v2.ID); err != nil {
		t.Fatal(err)
	}
	if body, _ := e.served(host, "index.html"); body != "v2" {
		t.Fatal("must serve v2 again")
	}
	other, _ := e.svc.CreateSite(ctx, u, "other", "")
	if _, err := e.svc.Rollback(ctx, u, other.Site.ID, v1.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("release of another site: %v", err)
	}
}

func TestPruneKeepsCurrentAndLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	ref := site.Site.ID
	var ids []string
	for i := range 6 {
		ids = append(ids, deploy(t, e, u, ref, fmt.Sprintf("v%d", i+1)).Deploy.ID)
		e.clock.Advance(time.Second)
	}
	ds, _, _ := e.svc.ListDeploys(ctx, u, ref)
	status := map[string]domain.DeployStatus{}
	for _, d := range ds {
		status[d.ID] = d.Status
	}
	if status[ids[0]] != domain.DeployPruned || status[ids[1]] != domain.DeployReady {
		t.Fatalf("oldest must be pruned: %v", status)
	}
	if _, err := os.Stat(filepath.Join(e.root, "sites", ref, "releases", ids[0])); !os.IsNotExist(err) {
		t.Fatal("pruned files must be deleted")
	}
	if _, err := e.svc.Rollback(ctx, u, ref, ids[0]); err == nil {
		t.Fatal("pruned release cannot be a rollback target")
	} else {
		wantCode(t, err, domain.CodeDeployNotReady)
	}
	// Roll back to the oldest ready release, then publish more: it must survive.
	if _, err := e.svc.Rollback(ctx, u, ref, ids[1]); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		deploy(t, e, u, ref, fmt.Sprintf("w%d", i))
		e.clock.Advance(time.Second)
	}
	_, _ = e.svc.Rollback(ctx, u, ref, ids[1])
	if d, err := e.repo.GetDeploy(ctx, ref, ids[1]); err == nil && d.Status == domain.DeployReady {
		t.Fatal("ids[1] was not current during the later deploys, so it should be pruned")
	}
}

func TestDeployRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	for range DeploysPerHour {
		deploy(t, e, u, site.Site.ID, "x")
	}
	_, err := e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "x"}), domain.SourceUI)
	de := domain.AsError(err)
	if de.Kind != domain.KindRateLimited || de.RetryAfter <= 0 {
		t.Fatalf("31st deploy: %v", err)
	}
}

func TestCrashDuringExtractionIsCleanedOnStart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	deploy(t, e, u, site.Site.ID, "v1")
	// Simulate a crash: a half-written staging dir and an upload left behind.
	staging, _ := e.svc.files.StagingDir(site.Site.ID, domain.NewDeployID(e.clock.Now()))
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(staging, "index.html"), []byte("half"), 0o644)
	_ = os.WriteFile(filepath.Join(e.svc.uploadDir, "upload-123"), []byte("x"), 0o644)
	// Simulate a lost by-host link.
	_ = os.Remove(filepath.Join(e.root, "by-host", site.Site.ID+".pp.test"))

	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatal("staging dir must be removed on start")
	}
	if m, _ := filepath.Glob(filepath.Join(e.svc.uploadDir, "upload-*")); len(m) != 0 {
		t.Fatal("uploads must be removed on start")
	}
	if body, _ := e.served(site.Site.ID+".pp.test", "index.html"); body != "v1" {
		t.Fatal("missing host link must be restored from the database")
	}
}

func TestExtractionFailureInjection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	deploy(t, e, u, site.Site.ID, "v1")
	e.svc.extract = func(ctx context.Context, src, dest string, lim archive.Limits, now time.Time) (archive.Result, error) {
		_ = os.MkdirAll(dest, 0o755)
		return archive.Result{}, errors.New("disk full")
	}
	if _, err := e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "v2"}), domain.SourceUI); err == nil {
		t.Fatal("must fail")
	}
	if body, _ := e.served(site.Site.ID+".pp.test", "index.html"); body != "v1" {
		t.Fatal("current release must stay")
	}
	entries, _ := os.ReadDir(filepath.Join(e.root, "sites", site.Site.ID, "releases"))
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".tmp") {
			t.Fatal("staging must be discarded")
		}
	}
}

func TestAuditTrail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "p", "")
	deploy(t, e, u, site.Site.ID, "v1")
	tok, _ := e.svc.CreateToken(ctx, u, "cli", 0)
	_, _ = e.svc.CreateSite(ctx, u, "", "") // rejected: must not be logged
	rows, total, err := e.svc.ListAudit(ctx, e.admin, AuditFilter{ActorID: &u.User.ID})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, r := range rows {
		actions[r.Action] = true
		if strings.Contains(fmt.Sprint(r.Meta), tok.Value) {
			t.Fatal("audit must never contain token values")
		}
	}
	for _, want := range []string{"user.activate", "site.create", "deploy.create", "token.create"} {
		if !actions[want] {
			t.Fatalf("missing %s in %v (total %d)", want, actions, total)
		}
	}
	if total != 4 {
		t.Fatalf("rejected actions must not be logged, got %d rows: %v", total, actions)
	}
	if _, _, err := e.svc.ListAudit(ctx, u, AuditFilter{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("audit is admin only")
	}
}

func BenchmarkDeploy20MB(b *testing.B) {
	t := &testing.T{}
	_ = t
	e := newEnv(&testing.T{})
	ctx := context.Background()
	u := e.admin
	site, err := e.svc.CreateSite(ctx, u, "bench", "")
	if err != nil {
		b.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 40*1024/16)
	for i := range 500 {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("assets/f%03d.bin", i), Method: zip.Store})
		_, _ = w.Write(chunk)
	}
	w, _ := zw.Create("index.html")
	_, _ = w.Write([]byte("hi"))
	_ = zw.Close()
	data := buf.Bytes()
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		e.svc.deployLimit.Reset(userKey(u.User.ID))
		if _, err := e.svc.Deploy(ctx, u, site.Site.ID, bytes.NewReader(data), domain.SourceAPI); err != nil {
			b.Fatal(err)
		}
		if d := time.Since(start); d > 5*time.Second {
			b.Fatalf("20 MB deploy took %v", d)
		}
	}
}
