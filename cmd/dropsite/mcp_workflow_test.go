package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/api"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp/remote"
	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/testenv"
	"github.com/b0balYson/dropsite/internal/visualdiff"
)

type whiteRenderer struct{}

func (whiteRenderer) Screenshot(context.Context, render.Shot) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 5))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	return visualdiff.EncodePNG(img)
}

// newRendererRig is newParityRig with a screenshot renderer.
func newRendererRig(t *testing.T) *parityRig {
	t.Helper()
	env := testenv.New(t, testenv.WithRenderer(whiteRenderer{}))
	r := &parityRig{t: t, env: env}
	r.api = httptest.NewServer(api.New(env.Svc, nil))
	t.Cleanup(r.api.Close)
	r.remote = httptest.NewServer(remote.New(env.Svc, remote.Options{}))
	t.Cleanup(r.remote.Close)
	return r
}

func TestStdioWorkflowTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Coffee", "coffee")
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.env.Svc.Deploy(ctx, ann, v.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "1"})), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
	r.env.Clock.Advance(time.Second)
	if _, err := r.env.Svc.Deploy(ctx, ann, v.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "2"})), domain.SourceUI); err != nil {
		t.Fatal(err)
	}

	cp := mustTool(t, local, "clone_site", map[string]any{"site": "coffee", "title": "Coffee 2"})
	if cp["site_id"] == v.Site.ID || cp["url"] == "" {
		t.Fatalf("clone: %v", cp)
	}
	ts := same(t, local, web, "list_templates", map[string]any{"lang": "en"})
	if len(ts["templates"].([]any)) != 5 {
		t.Fatalf("templates: %v", ts)
	}
	if out := mustTool(t, local, "create_site_from_template", map[string]any{"template": "event", "title": "Fest"}); out["url"] == "" {
		t.Fatalf("template: %v", out)
	}
	same(t, local, web, "create_site_from_template", map[string]any{"template": "shop"})
	wantToolErr(t, local, "create_site_from_template", map[string]any{"template": "shop"}, "list_templates")

	at := r.env.Clock.Now().Add(time.Hour).Format(time.RFC3339)
	same(t, local, web, "schedule_publish", map[string]any{"publish_at": at})
	same(t, local, web, "schedule_publish", map[string]any{"publish_at": "tomorrow", "site": "coffee", "deploy_id": first.Deploy.ID})
	sch := mustTool(t, local, "schedule_publish", map[string]any{"publish_at": at, "site": "coffee", "deploy_id": first.Deploy.ID})
	if sch["status"] != "pending" || sch["deploy_id"] != first.Deploy.ID {
		t.Fatalf("schedule: %v", sch)
	}
	same(t, local, web, "list_scheduled_publishes", map[string]any{})
	same(t, local, web, "list_scheduled_publishes", map[string]any{"site": "coffee"})
	if out := mustTool(t, web, "cancel_scheduled_publish", map[string]any{"schedule_id": sch["schedule_id"]}); out["status"] != "cancelled" {
		t.Fatalf("cancel: %v", out)
	}
	same(t, local, web, "cancel_scheduled_publish", map[string]any{"schedule_id": sch["schedule_id"]})
	same(t, local, web, "cancel_scheduled_publish", map[string]any{"schedule_id": "999"})
	wantToolErr(t, local, "cancel_scheduled_publish", map[string]any{"schedule_id": "999"}, "list_scheduled_publishes")

	// Without a renderer both servers answer the same "not available".
	same(t, local, web, "screenshot_site", map[string]any{"site": "coffee"})
	wantToolErr(t, local, "screenshot_site", map[string]any{"site": "coffee"}, "renderer_unavailable")
	same(t, local, web, "compare_versions", map[string]any{"site": "coffee", "from_deploy_id": first.Deploy.ID})
}

func imageOf(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) *sdk.ImageContent {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("%s: %v %+v", name, err, res)
	}
	for _, c := range res.Content {
		if img, ok := c.(*sdk.ImageContent); ok {
			return img
		}
	}
	t.Fatalf("%s: no image in %+v", name, res.Content)
	return nil
}

func TestStdioScreenshotTools(t *testing.T) {
	r := newRendererRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Coffee", "coffee")
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.env.Svc.Deploy(ctx, ann, v.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "1"})), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
	shot := same(t, local, web, "screenshot_site", map[string]any{"site": "coffee", "device": "mobile"})
	if shot["deploy_id"] != res.Deploy.ID || shot["width"] != float64(390) || shot["site_id"] != v.Site.ID {
		t.Fatalf("shot: %v", shot)
	}
	for _, cs := range []*sdk.ClientSession{local, web} {
		if img := imageOf(t, cs, "screenshot_site", map[string]any{"site": "coffee"}); img.MIMEType != "image/png" ||
			!strings.HasPrefix(string(img.Data), "\x89PNG") {
			t.Fatalf("image %s", img.MIMEType)
		}
	}
	diff := same(t, local, web, "compare_versions", map[string]any{"site": "coffee", "from_deploy_id": res.Deploy.ID})
	if diff["changed_percent"] != float64(0) || !strings.Contains(diff["summary"].(string), "same") {
		t.Fatalf("diff: %v", diff)
	}
	imageOf(t, local, "compare_versions", map[string]any{"site": "coffee", "from_deploy_id": res.Deploy.ID})
}
