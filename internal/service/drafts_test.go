package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

func (e *env) draft(a Actor, id, body string) DeployResult {
	e.t.Helper()
	e.clock.Advance(time.Second)
	res, err := e.svc.DeployWith(context.Background(), a, id, zipOf(e.t, map[string]string{"index.html": body}),
		domain.SourceAPI, PublishOptions{Draft: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) draftHost(id string) string { return domain.DraftPreviewLabel(id) + "." + testBase }

func (e *env) deployStatus(a Actor, siteID, deployID string) domain.DeployStatus {
	e.t.Helper()
	ds, _, err := e.svc.ListDeploys(context.Background(), a, siteID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, d := range ds {
		if d.ID == deployID {
			return d.Status
		}
	}
	e.t.Fatalf("release %s not listed", deployID)
	return ""
}

func TestDraftDoesNotChangeTheSite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, live := e.published(u, shop)

	res := e.draft(u, id, "<p>draft</p>")
	if e.current(u, id) != live || res.Site.Current == nil || res.Site.Current.ID != live {
		t.Fatal("a draft must not go live")
	}
	if res.Site.Draft == nil || res.Site.Draft.Deploy.ID != res.Deploy.ID {
		t.Fatalf("draft view %+v", res.Site.Draft)
	}
	if want := "https://" + e.draftHost(id) + "/"; res.Site.Draft.PreviewURL != want {
		t.Fatalf("preview url %q, want %q", res.Site.Draft.PreviewURL, want)
	}
	if res.Quality == nil {
		t.Fatal("a draft gets a quality report")
	}
	if got, _ := e.served(host, "index.html"); got != shop["index.html"] {
		t.Fatalf("site serves %q", got)
	}
	if got, err := e.served(e.draftHost(id), "index.html"); err != nil || got != "<p>draft</p>" {
		t.Fatalf("draft preview serves %q %v", got, err)
	}
	if e.auditCount("deploy.create") != 2 {
		t.Fatal("draft not audited")
	}
	// Strangers see nothing.
	_, err := e.svc.PublishDraft(ctx, e.user("x@pp.test"), id)
	wantNotFound(t, err)
	_, err = e.svc.DiscardDraft(ctx, e.user("y@pp.test"), id)
	wantNotFound(t, err)
}

func TestDraftIsCheckedLikeAPublish(t *testing.T) {
	e := newEnv(t)
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	_, err := e.svc.DeployWith(context.Background(), u, id,
		zipOf(t, map[string]string{"index.html": "<script>const k='" + fakeStripeKey() + "'</script>"}),
		domain.SourceAPI, PublishOptions{Draft: true})
	wantCode(t, err, domain.CodeSecretsFound)
	if v, _ := e.svc.GetSite(context.Background(), u, id); v.Draft != nil {
		t.Fatal("a refused draft was kept")
	}
}

func TestDraftReplacePublishDiscard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, live := e.published(u, shop)

	first := e.draft(u, id, "one").Deploy.ID
	second := e.draft(u, id, "two").Deploy.ID
	if e.deployStatus(u, id, first) != domain.DeployPruned {
		t.Fatal("a replaced draft must be pruned")
	}
	if got, _ := e.served(e.draftHost(id), "index.html"); got != "two" {
		t.Fatalf("preview serves %q", got)
	}

	// A direct publish keeps the draft.
	direct := e.redeploy(u, id, "direct")
	v, _ := e.svc.GetSite(ctx, u, id)
	if v.Draft == nil || v.Draft.Deploy.ID != second {
		t.Fatal("a direct publish must keep the draft")
	}

	res, err := e.svc.PublishDraft(ctx, u, id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.ID != second || e.current(u, id) != second || res.Site.Draft != nil {
		t.Fatalf("publish %+v", res)
	}
	if got, _ := e.served(host, "index.html"); got != "two" {
		t.Fatalf("site serves %q", got)
	}
	if _, err := e.served(e.draftHost(id), "index.html"); err == nil {
		t.Fatal("the preview must serve nothing without a draft")
	}
	if !e.hostExists(e.draftHost(id)) {
		t.Fatal("the preview address must stay allocated")
	}
	_, err = e.svc.PublishDraft(ctx, u, id)
	wantCode(t, err, domain.CodeNoDraft)
	if e.auditCount("draft.publish") != 1 {
		t.Fatal("publish not audited")
	}
	// The previous live release is still there for a rollback.
	if e.deployStatus(u, id, direct) != domain.DeployReady || e.deployStatus(u, id, live) != domain.DeployReady {
		t.Fatal("publishing a draft must keep earlier releases")
	}

	third := e.draft(u, id, "three").Deploy.ID
	if _, err := e.svc.DiscardDraft(ctx, u, id); err != nil {
		t.Fatal(err)
	}
	if e.deployStatus(u, id, third) != domain.DeployPruned || e.current(u, id) != second {
		t.Fatal("discard must prune the draft and keep the site")
	}
	_, err = e.svc.DiscardDraft(ctx, u, id)
	wantCode(t, err, domain.CodeNoDraft)
	if e.auditCount("draft.discard") != 1 {
		t.Fatal("discard not audited")
	}
}

func TestDraftSurvivesPruneAndRollback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	d := e.draft(u, id, "draft").Deploy.ID
	var last string
	for i := range config.DefaultKeepReleases + 2 {
		last = e.redeploy(u, id, fmt.Sprintf("v%d", i))
	}
	if e.deployStatus(u, id, d) != domain.DeployReady {
		t.Fatal("pruning removed the draft")
	}
	res, err := e.svc.Rollback(ctx, u, id, "")
	if err != nil || res.Deploy.ID == d || res.Deploy.ID == last {
		t.Fatalf("rollback picked %v %v", res.Deploy.ID, err)
	}
	// Rolling back onto the draft ends it.
	if _, err := e.svc.Rollback(ctx, u, id, d); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.GetSite(ctx, u, id); v.Draft != nil {
		t.Fatal("a draft that went live is no draft")
	}
	_, err = e.svc.PublishDraft(ctx, u, id)
	wantCode(t, err, domain.CodeNoDraft)
}

func TestEditsBuildOnTheDraft(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, live := e.published(u, shop)
	first, err := e.svc.UpdateSiteFilesWith(ctx, u, id, live,
		FileChanges{Put: []InlineFile{{Path: "index.html", Content: "draft 1"}}}, PublishOptions{Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.current(u, id) != live {
		t.Fatal("a draft edit went live")
	}
	e.clock.Advance(time.Second)
	second, err := e.svc.UpdateSiteFilesWith(ctx, u, id, first.Deploy.ID,
		FileChanges{Put: []InlineFile{{Path: "about.html", Content: "about"}}}, PublishOptions{Draft: true})
	if err != nil {
		t.Fatalf("editing the draft: %v", err)
	}
	if got, _ := e.served(e.draftHost(id), "about.html"); got != "about" {
		t.Fatalf("draft lacks the new file: %q", got)
	}
	if got, _ := e.served(e.draftHost(id), "index.html"); got != "draft 1" {
		t.Fatalf("draft lost the first edit: %q", got)
	}
	if e.deployStatus(u, id, first.Deploy.ID) != domain.DeployPruned {
		t.Fatal("the draft it replaced must be pruned")
	}
	// An unrelated base still conflicts.
	_, err = e.svc.UpdateSiteFilesWith(ctx, u, id, first.Deploy.ID,
		FileChanges{Put: []InlineFile{{Path: "x.html", Content: "x"}}}, PublishOptions{Draft: true})
	wantCode(t, err, domain.CodeDeployConflict)

	// Uploads close into drafts too.
	up, err := e.svc.BeginUpload(ctx, u, id, "", UploadPatch, second.Deploy.ID)
	if err != nil {
		t.Fatalf("patch upload on the draft: %v", err)
	}
	if _, _, err := e.svc.UploadFiles(ctx, u, up.ID, []InlineFile{{Path: "big.html", Content: "big"}}, nil); err != nil {
		t.Fatal(err)
	}
	third, err := e.svc.CommitUploadWith(ctx, u, up.ID, nil, PublishOptions{Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.GetSite(ctx, u, id)
	if v.Draft == nil || v.Draft.Deploy.ID != third.Deploy.ID || e.current(u, id) != live {
		t.Fatalf("upload draft %+v", v.Draft)
	}
	if got, _ := e.served(e.draftHost(id), "about.html"); got != "about" {
		t.Fatal("the upload lost the draft's files")
	}
}

func TestDraftPreviewOfProtectedSite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	if _, err := e.svc.UpdateSite(ctx, u, id, SiteUpdate{Password: sptr("visitor-pass-1")}); err != nil {
		t.Fatal(err)
	}
	e.draft(u, id, "secret draft")
	host := e.draftHost(id)
	if !e.marker("protected", host) {
		t.Fatal("the draft preview of a protected site must be protected")
	}
	if d, err := e.svc.CheckVisitorAccess(ctx, host, ""); err != nil || d != AccessLogin {
		t.Fatalf("preview without cookie: %v %v", d, err)
	}
	c, err := e.visitorLogin(host, "visitor-pass-1", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.CheckVisitorAccess(ctx, host, c.Value); d != AccessAllow {
		t.Fatalf("cookie on preview: %v", d)
	}
	// Removing the password unmarks it; a preview-shaped name of another
	// site without drafts is unknown.
	if _, err := e.svc.UpdateSite(ctx, u, id, SiteUpdate{Protected: bptr(false)}); err != nil {
		t.Fatal(err)
	}
	if e.marker("protected", host) {
		t.Fatal("marker kept after unprotect")
	}
	other, _ := e.svc.CreateSite(ctx, u, "o", "")
	if d, _ := e.svc.CheckVisitorAccess(ctx, e.draftHost(other.Site.ID), ""); d != AccessUnknown {
		t.Fatalf("site without drafts: %v", d)
	}
	if !e.svc.isPreviewHost(host) {
		t.Fatal("draft preview visits must not count as site visits")
	}
}

func TestDraftPreviewLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	e.draft(u, id, "draft")
	host := e.draftHost(id)

	// Reconciliation keeps the preview (also in wildcard mode) and repairs it.
	if err := e.svc.files.UnlinkHost(host); err != nil {
		t.Fatal(err)
	}
	e.restart(config.TLSModeWildcard)
	if got, _ := e.served(host, "index.html"); got != "draft" {
		t.Fatalf("reconcile did not restore the preview: %q", got)
	}
	e.restart(config.TLSModeOnDemand)
	if got, _ := e.served(host, "index.html"); got != "draft" {
		t.Fatalf("ondemand mode keeps the draft preview: %q", got)
	}

	if err := e.svc.DeleteSite(ctx, u, id); err != nil {
		t.Fatal(err)
	}
	if e.hostExists(host) {
		t.Fatal("deleting the site must remove its preview")
	}
	e.restart(config.TLSModeOnDemand)
	if e.hostExists(host) {
		t.Fatal("reconcile restored the preview of a deleted site")
	}
}

func TestLegacySlugShadowsDraftPreview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	a, _ := e.svc.CreateSite(ctx, u, "a", "")
	b, _ := e.svc.CreateSite(ctx, u, "b", "")
	deploy(t, e, u, a.Site.ID, "site a")
	deploy(t, e, u, b.Site.ID, "site b")
	host := e.draftHost(a.Site.ID)
	if _, err := e.repo.SetSlugHost(ctx, b.Site.ID,
		&domain.Hostname{Hostname: host, SiteID: b.Site.ID, Kind: domain.HostSlug, Verified: true, CreatedAt: e.clock.Now()},
		e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.files.LinkHost(host, b.Site.ID); err != nil {
		t.Fatal(err)
	}
	res := e.draft(u, a.Site.ID, "draft a")
	if res.Site.Draft == nil || res.Site.Draft.PreviewURL != "" {
		t.Fatalf("shadowed draft %+v", res.Site.Draft)
	}
	e.restart(config.TLSModeOnDemand)
	if got, _ := e.served(host, "index.html"); got != "site b" {
		t.Fatalf("the slug must win, served %q", got)
	}
	if _, err := e.svc.PublishDraft(ctx, u, a.Site.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.served(host, "index.html"); got != "site b" {
		t.Fatalf("publishing touched the slug, served %q", got)
	}
}

// A draft that went live through a scheduled publish is no longer a draft:
// a later rollback must not bring it back as one.
func TestScheduledDraftIsNoLongerADraft(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, live := e.published(u, shop)
	draft := e.draft(u, id, "sale").Deploy.ID

	if _, err := e.svc.ScheduleRelease(ctx, u, id, draft, e.clock.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	if err := e.svc.RunDueSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	if e.current(u, id) != draft {
		t.Fatal("the scheduled draft is not live")
	}
	if _, err := e.svc.Rollback(ctx, u, id, live); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.GetSite(ctx, u, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Draft != nil {
		t.Fatalf("a published draft came back after a rollback: %+v", v.Draft)
	}
	if _, err := e.served(e.draftHost(id), "index.html"); err == nil {
		t.Fatal("the preview must serve nothing once the draft went live")
	}
}
