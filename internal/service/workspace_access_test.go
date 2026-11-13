package service

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// siteOp is one operation on a site, called with the site's ID.
type siteOp struct {
	name string
	call func(ctx context.Context, a Actor, ref string) error
}

// workspaceSiteOps covers every kind of right on a site: whatever passes
// resolveSite for a member must be "not found" for a stranger.
func workspaceSiteOps(deployID, laterID string) []siteOp {
	return []siteOp{
		{"get", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).GetSite(ctx, a, ref)
			return err
		}},
		{"list deploys", func(ctx context.Context, a Actor, ref string) error {
			_, _, err := svcOf(ctx).ListDeploys(ctx, a, ref)
			return err
		}},
		{"update", func(ctx context.Context, a Actor, ref string) error {
			title := "Renamed"
			_, err := svcOf(ctx).UpdateSite(ctx, a, ref, SiteUpdate{Title: &title})
			return err
		}},
		{"rollback", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).Rollback(ctx, a, ref, deployID)
			return err
		}},
		{"site files", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ListSiteFiles(ctx, a, ref, "", "")
			return err
		}},
		{"read files", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ReadSiteFiles(ctx, a, ref, "", []string{"index.html"})
			return err
		}},
		{"begin upload", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).BeginUpload(ctx, a, ref, "", UploadReplace, "")
			return err
		}},
		{"publish settings", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).SetPublishSettings(ctx, a, ref, true)
			return err
		}},
		{"quality report", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).GetDeployReport(ctx, a, ref, deployID)
			return err
		}},
		{"scores", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).DeployScores(ctx, a, ref)
			return err
		}},
		{"form settings", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).SetFormSiteSettings(ctx, a, ref, false)
			return err
		}},
		{"form channels", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ListFormChannels(ctx, a, ref)
			return err
		}},
		{"submissions", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).SearchFormSubmissions(ctx, a, ref, SubmissionQuery{})
			return err
		}},
		{"export submissions", func(ctx context.Context, a Actor, ref string) error {
			return svcOf(ctx).ExportFormSubmissions(ctx, a, ref, "", func(string) io.Writer { return io.Discard })
		}},
		{"collections", func(ctx context.Context, a Actor, ref string) error {
			_, _, err := svcOf(ctx).ListCollections(ctx, a, ref)
			return err
		}},
		{"documents", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).SaveDocuments(ctx, a, ref, "menu", []DocumentInput{doc("", `{"x":1}`)}, false)
			return err
		}},
		{"proxy", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ListProxyEndpoints(ctx, a, ref)
			return err
		}},
		{"proxy put", func(ctx context.Context, a Actor, ref string) error {
			_, _, err := svcOf(ctx).PutProxyEndpoint(ctx, a, ref, "ai", openAIConfig())
			return err
		}},
		{"hostnames", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ListHostnames(ctx, a, ref)
			return err
		}},
		{"add domain", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).AddDomain(ctx, a, ref, "shop.example.com")
			return err
		}},
		{"counters", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).GetSiteCounters(ctx, a, ref)
			return err
		}},
		{"stats", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).GetSiteStats(ctx, a, ref, 7)
			return err
		}},
		{"attachments", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).SiteAttachments(ctx, a, ref)
			return err
		}},
		{"schedules", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ListSchedules(ctx, a, ref)
			return err
		}},
		{"schedule release", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).ScheduleRelease(ctx, a, ref, laterID, svcOf(ctx).Now().Add(time.Hour))
			return err
		}},
		{"screenshot", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).Screenshot(ctx, a, ref, "", "")
			return err
		}},
		{"clone", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).CloneSite(ctx, a, ref, "", "", domain.SourceAPI)
			return err
		}},
		// Changes merged after workspaces rely on resolveSite too.
		{"publish draft", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).PublishDraft(ctx, a, ref)
			return err
		}},
		{"discard draft", func(ctx context.Context, a Actor, ref string) error {
			_, err := svcOf(ctx).DiscardDraft(ctx, a, ref)
			return err
		}},
		{"sheet link", func(ctx context.Context, a Actor, ref string) error {
			_, _, err := svcOf(ctx).PutSheetLink(ctx, a, ref, "menu",
				SheetLinkInput{URL: "https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789/edit"})
			return err
		}},
	}
}

type svcKey struct{}

func svcOf(ctx context.Context) *Service { return ctx.Value(svcKey{}).(*Service) }

func TestWorkspaceSiteRights(t *testing.T) {
	e := newEnv(t)
	ctx := context.WithValue(context.Background(), svcKey{}, e.svc)
	ann, bob, eve, ws := team(t, e)
	v, err := e.svc.CreateSiteIn(ctx, ann, "Shop", "shop", ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := deploy(t, e, ann, v.Site.ID, "<h1>one</h1>")
	second := deploy(t, e, ann, v.Site.ID, "<h1>two</h1>")
	e.svc.shooter = &fakeRenderer{svc: e.svc}
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu", Mode: domain.CollectionPublic}); err != nil {
		t.Fatal(err)
	}
	ops := workspaceSiteOps(first.Deploy.ID, second.Deploy.ID)
	for _, op := range ops {
		if err := op.call(ctx, eve, v.Site.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: stranger got %v, want not found", op.name, err)
		}
	}
	for _, op := range ops {
		if err := op.call(ctx, bob, v.Site.ID); errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: member got not found", op.name)
		}
	}
	// A stranger's list of scheduled publishes does not show the site's.
	if rows, err := e.svc.ListSchedules(ctx, eve, ""); err != nil || len(rows) != 0 {
		t.Fatalf("stranger schedules %v %v", rows, err)
	}
	if rows, err := e.svc.ListSchedules(ctx, bob, ""); err != nil || len(rows) != 1 {
		t.Fatalf("member schedules %v %v", rows, err)
	}

	// The creator who left loses access, and the remaining member deletes the site.
	if _, err := e.svc.RemoveWorkspaceMember(ctx, ann, ws.ID, ann.User.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetSite(ctx, ann, v.Site.ID)
	wantNotFound(t, err)
	wantNotFound(t, e.svc.DeleteSite(ctx, eve, v.Site.ID))
	if err := e.svc.DeleteSite(ctx, bob, v.Site.ID); err != nil {
		t.Fatal(err)
	}
}
