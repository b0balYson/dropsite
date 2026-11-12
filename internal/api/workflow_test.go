package api

import (
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/testenv"
	"github.com/b0balYson/dropsite/internal/visualdiff"
)

// pngRenderer returns the same small image for every page.
type pngRenderer struct{}

func (pngRenderer) Screenshot(context.Context, render.Shot) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	return visualdiff.EncodePNG(img)
}

func (h *harness) publish(token string, files map[string]string) deployResultDTO {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/v1/sites", token, map[string]string{"title": "Shop"})
	wantStatus(h.t, rec, http.StatusCreated)
	site := decodeAs[siteDTO](h.t, rec)
	rec = h.deploy(site.ID, token, testenv.Zip(h.t, files))
	wantStatus(h.t, rec, http.StatusCreated)
	return decodeAs[deployResultDTO](h.t, rec)
}

func TestCloneAndTemplatesAPI(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("u@pp.test")
	_, other := h.user("x@pp.test")
	src := h.publish(tok, index("<p>hello</p>"))

	rec := h.do(http.MethodPost, "/api/v1/sites/"+src.Site.ID+"/clone", tok, map[string]string{"title": "Copy"},
		withHeader(clientHeader, "cli/1.0"))
	wantStatus(t, rec, http.StatusCreated)
	res := decodeAs[deployResultDTO](t, rec)
	if res.Site.ID == src.Site.ID || res.Site.Title != "Copy" || res.Deploy.Source != "cli" {
		t.Fatalf("clone %+v", res.Site)
	}
	wantStatus(t, h.do(http.MethodPost, "/api/v1/sites/"+src.Site.ID+"/clone", other, nil), http.StatusNotFound)

	rec = h.do(http.MethodGet, "/api/v1/templates?lang=en", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	l := decodeAs[list[templateDTO]](t, rec)
	if l.Total != 5 || l.Items[0].Name != "cafe" || l.Items[0].Title == "" {
		t.Fatalf("templates %+v", l)
	}
	rec = h.do(http.MethodPost, "/api/v1/templates/links/sites", tok, map[string]string{"title": "Me", "lang": "en"})
	wantStatus(t, rec, http.StatusCreated)
	if r := decodeAs[deployResultDTO](t, rec); r.Site.Title != "Me" {
		t.Fatalf("template site %+v", r.Site)
	}
	rec = h.do(http.MethodPost, "/api/v1/templates/shop/sites", tok, nil)
	wantStatus(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "template_not_found") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestSchedulesAPI(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("u@pp.test")
	_, other := h.user("x@pp.test")
	first := h.publish(tok, index("<p>1</p>"))
	h.env.Clock.Advance(time.Second)
	wantStatus(t, h.deploy(first.Site.ID, tok, testenv.Zip(t, index("<p>2</p>"))), http.StatusCreated)
	at := h.env.Clock.Now().Add(time.Hour).In(time.FixedZone("MSK", 3*3600)).Format(time.RFC3339)

	path := "/api/v1/sites/" + first.Site.ID + "/schedules"
	rec := h.do(http.MethodPost, path, tok, map[string]string{"deploy_id": first.DeployID, "publish_at": "2026-12-01T09:00:00"})
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "schedule_invalid") {
		t.Fatalf("body %s", rec.Body.String())
	}
	wantStatus(t, h.do(http.MethodPost, path, other, map[string]string{"deploy_id": first.DeployID, "publish_at": at}),
		http.StatusNotFound)
	rec = h.do(http.MethodPost, path, tok, map[string]string{"deploy_id": first.DeployID, "publish_at": at})
	wantStatus(t, rec, http.StatusCreated)
	sch := decodeAs[scheduleDTO](t, rec)
	if sch.Status != "pending" || sch.DeployID != first.DeployID || sch.SiteURL == "" {
		t.Fatalf("schedule %+v", sch)
	}

	rec = h.do(http.MethodGet, "/api/v1/schedules", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	if l := decodeAs[list[scheduleDTO]](t, rec); l.Total != 1 {
		t.Fatalf("list %+v", l)
	}
	if l := decodeAs[list[scheduleDTO]](t, h.do(http.MethodGet, "/api/v1/schedules", other, nil)); l.Total != 0 {
		t.Fatalf("other sees %+v", l)
	}
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/schedules/"+sch.ID, other, nil), http.StatusNotFound)
	rec = h.do(http.MethodDelete, "/api/v1/schedules/"+sch.ID, tok, nil)
	wantStatus(t, rec, http.StatusOK)
	if s := decodeAs[scheduleDTO](t, rec); s.Status != "cancelled" {
		t.Fatalf("cancel %+v", s)
	}
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/schedules/"+sch.ID, tok, nil), http.StatusConflict)
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/schedules/abc", tok, nil), http.StatusNotFound)

	// An upload session becomes a prepared release.
	rec = h.do(http.MethodPost, "/api/v1/uploads", tok, map[string]string{"site": first.Site.ID})
	wantStatus(t, rec, http.StatusCreated)
	up := decodeAs[map[string]any](t, rec)
	id, _ := up["id"].(string)
	rec = h.do(http.MethodPost, "/api/v1/uploads/"+id+"/files", tok, map[string]any{
		"files": []map[string]string{{"path": "index.html", "content": "<p>3</p>"}},
	})
	wantStatus(t, rec, http.StatusOK)
	rec = h.do(http.MethodPost, "/api/v1/uploads/"+id+"/schedule", tok, map[string]string{"publish_at": at})
	wantStatus(t, rec, http.StatusCreated)
	if s := decodeAs[scheduleDTO](t, rec); s.Status != "pending" || s.DeployID == "" {
		t.Fatalf("upload schedule %+v", s)
	}
}

func TestScreenshotAPI(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("u@pp.test")
	site := h.publish(tok, index("<p>1</p>"))
	rec := h.do(http.MethodGet, "/api/v1/sites/"+site.Site.ID+"/screenshot", tok, nil)
	wantStatus(t, rec, http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), "renderer_unavailable") {
		t.Fatalf("body %s", rec.Body.String())
	}

	h = newHarness(t, testenv.WithRenderer(pngRenderer{}))
	_, tok = h.user("u@pp.test")
	site = h.publish(tok, index("<p>1</p>"))
	rec = h.do(http.MethodGet, "/api/v1/sites/"+site.Site.ID+"/screenshot?device=mobile", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Dropsite-Deploy-Id") != site.DeployID ||
		!strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Fatalf("screenshot headers %v", rec.Header())
	}
	rec = h.do(http.MethodGet, "/api/v1/sites/"+site.Site.ID+"/visual-diff?from="+site.DeployID, tok, nil)
	wantStatus(t, rec, http.StatusOK)
	d := decodeAs[visualDiffDTO](t, rec)
	img, err := base64.StdEncoding.DecodeString(d.ImagePNG)
	if err != nil || d.ChangedPercent != 0 || d.ToDeployID != site.DeployID || !strings.HasPrefix(string(img), "\x89PNG") {
		t.Fatalf("diff %+v %v", d.ChangedPercent, err)
	}
}
