package ui

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
	"github.com/b0balYson/dropsite/internal/visualdiff"
)

func domainPage() service.Page { return service.Page{Limit: service.MaxPageSize} }

func idOf(id int64) string { return strconv.FormatInt(id, 10) }

type grayRenderer struct{}

func (grayRenderer) Screenshot(context.Context, render.Shot) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.Gray{Y: 128}}, image.Point{}, draw.Src)
	return visualdiff.EncodePNG(img)
}

func TestCreateSiteFromTemplatePage(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	p := h.get("/")
	wantContains(t, p.body, `id="templates"`, `value="cafe"`, `value="links"`, "Начать с шаблона")
	p = h.submit("/", "/sites/from-template", url.Values{"template": {"event"}, "title": {"Фестиваль"}, "lang": {"ru"}})
	wantStatus(t, p, http.StatusSeeOther)
	sites, _, err := h.env.Svc.ListSites(context.Background(), alice, domainPage())
	if err != nil || len(sites) != 1 || sites[0].Title != "Фестиваль" || sites[0].LastDeployAt == nil {
		t.Fatalf("sites %+v %v", sites, err)
	}
	wantStatus(t, h.submit("/", "/sites/from-template", url.Values{"template": {"shop"}}), http.StatusNotFound)
}

func TestSiteWorkflowSection(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	page := "/sites/" + v.Site.ID
	wantNotContains(t, h.get(page).body, "Сделать копию", "data-tz-form")

	first := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	h.env.Clock.Advance(time.Second)
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "two"})
	body := h.get(page).body
	wantContains(t, body, "Копия, расписание и снимок", "Сделать копию", "data-tz-form", first.ID, "workflow.js")
	wantNotContains(t, body, "screenshot.png", "compare.png")

	at := h.env.Clock.Now().Add(2 * time.Hour).Add(3 * time.Hour) // local time in UTC+3
	p := h.submit(page, page+"/schedules", url.Values{
		"deploy": {first.ID}, "publish_at": {at.Format(localTimeLayout)}, "tz_offset": {"-180"},
	})
	wantStatus(t, p, http.StatusSeeOther)
	scheds, err := h.env.Svc.ListSchedules(context.Background(), alice, v.Site.ID)
	if err != nil || len(scheds) != 1 || !scheds[0].PublishAt.Equal(at.Add(-3*time.Hour).Truncate(time.Minute)) {
		t.Fatalf("schedules %+v %v", scheds, err)
	}
	body = h.get(page).body
	wantContains(t, body, "ждёт", "/schedules/")
	p = h.follow(h.submit(page, page+"/schedules", url.Values{"deploy": {first.ID}, "publish_at": {"later"}}))
	wantContains(t, p.body, "Выберите дату и время")

	p = h.submit(page, page+"/schedules/"+idOf(scheds[0].ID)+"/cancel", nil)
	wantStatus(t, p, http.StatusSeeOther)
	if s, _ := h.env.Svc.ListSchedules(context.Background(), alice, v.Site.ID); s[0].Status != domain.ScheduleCancelled {
		t.Fatalf("not cancelled: %+v", s)
	}

	p = h.submit(page, page+"/clone", url.Values{"title": {"Promo 2"}})
	wantStatus(t, p, http.StatusSeeOther)
	if !strings.HasPrefix(p.location(), "/sites/") || p.location() == page {
		t.Fatalf("clone redirect %q", p.location())
	}
	wantStatus(t, h.get(page+"/screenshot.png"), http.StatusServiceUnavailable)
}

func TestSiteScreenshotWithRenderer(t *testing.T) {
	h := newHarness(t, testenv.WithRenderer(grayRenderer{}))
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	first := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "one"})
	h.env.Clock.Advance(time.Second)
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "two"})
	page := "/sites/" + v.Site.ID
	wantContains(t, h.get(page).body, page+"/screenshot.png", page+"/compare.png")
	p := h.get(page + "/screenshot.png?device=mobile")
	wantStatus(t, p, http.StatusOK)
	if p.resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("type %q", p.resp.Header.Get("Content-Type"))
	}
	p = h.get(page + "/compare.png?from=" + first.ID)
	wantStatus(t, p, http.StatusOK)
	if p.resp.Header.Get("X-Dropsite-Changed-Percent") != "0.00" {
		t.Fatalf("changed %q", p.resp.Header.Get("X-Dropsite-Changed-Percent"))
	}
	bob := h.env.User("bob@pp.test")
	other := h.createSite(bob, "Bob", "")
	h.deploy(bob, other.Site.ID, map[string]string{"index.html": "b"})
	wantStatus(t, h.get("/sites/"+other.Site.ID+"/screenshot.png"), http.StatusNotFound)
}
