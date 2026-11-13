package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Site copies, templates, scheduled publishes and screenshots (change
// chat-workflow).

func (a *api) workflowRoutes() []route {
	return []route{
		{Method: http.MethodPost, Path: "/sites/{ref}/clone", handle: a.cloneSite},
		{Method: http.MethodGet, Path: "/templates", handle: a.listTemplates},
		{Method: http.MethodPost, Path: "/templates/{name}/sites", handle: a.createFromTemplate},
		{Method: http.MethodGet, Path: "/schedules", handle: a.listSchedules},
		{Method: http.MethodPost, Path: "/sites/{ref}/schedules", handle: a.scheduleRelease},
		{Method: http.MethodPost, Path: "/uploads/{id}/schedule", handle: a.scheduleUpload},
		{Method: http.MethodDelete, Path: "/schedules/{id}", handle: a.cancelSchedule},
		{Method: http.MethodGet, Path: "/sites/{ref}/screenshot", handle: a.screenshot},
		{Method: http.MethodGet, Path: "/sites/{ref}/visual-diff", handle: a.visualDiff},
	}
}

type newSiteRequest struct {
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Lang        string `json:"lang"`
	WorkspaceID string `json:"workspace_id"`
}

func (a *api) cloneSite(w http.ResponseWriter, r *http.Request) error {
	var req newSiteRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	ws, err := workspaceField(req.WorkspaceID)
	if err != nil {
		return err
	}
	res, err := a.svc.CloneSiteIn(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), req.Title, req.Slug, ws, deploySource(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toDeployResult(res))
	return nil
}

type templateDTO struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (a *api) listTemplates(w http.ResponseWriter, r *http.Request) error {
	ts := a.svc.ListTemplates(r.URL.Query().Get("lang"))
	out := make([]templateDTO, 0, len(ts))
	for _, t := range ts {
		out = append(out, templateDTO{Name: t.Name, Title: t.Title, Description: t.Description})
	}
	writeJSON(w, http.StatusOK, newList(out, len(out)))
	return nil
}

func (a *api) createFromTemplate(w http.ResponseWriter, r *http.Request) error {
	var req newSiteRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	ws, err := workspaceField(req.WorkspaceID)
	if err != nil {
		return err
	}
	res, err := a.svc.CreateSiteFromTemplateIn(r.Context(), actorFrom(r.Context()), r.PathValue("name"),
		req.Title, req.Slug, req.Lang, ws, deploySource(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toDeployResult(res))
	return nil
}

type scheduleDTO struct {
	ID         string     `json:"id"`
	SiteID     string     `json:"site_id"`
	SiteTitle  string     `json:"site_title"`
	SiteURL    string     `json:"site_url"`
	DeployID   string     `json:"deploy_id"`
	PublishAt  time.Time  `json:"publish_at"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	PreviewURL *string    `json:"preview_url"`
}

func toSchedule(v service.ScheduleView) scheduleDTO {
	return scheduleDTO{
		ID: idString(v.ID), SiteID: v.SiteID, SiteTitle: v.SiteTitle, SiteURL: v.SiteURL, DeployID: v.DeployID,
		PublishAt: utc(v.PublishAt), Status: string(v.Status), Error: v.Error, CreatedAt: utc(v.CreatedAt),
		FinishedAt: utcPtr(v.FinishedAt), PreviewURL: optional(v.PreviewURL),
	}
}

func (a *api) listSchedules(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.svc.ListSchedules(r.Context(), actorFrom(r.Context()), r.URL.Query().Get("site"))
	if err != nil {
		return err
	}
	out := make([]scheduleDTO, 0, len(rows))
	for _, v := range rows {
		out = append(out, toSchedule(v))
	}
	writeJSON(w, http.StatusOK, newList(out, len(out)))
	return nil
}

type scheduleRequest struct {
	DeployID  string `json:"deploy_id"`
	PublishAt string `json:"publish_at"`
}

func (a *api) scheduleRelease(w http.ResponseWriter, r *http.Request) error {
	var req scheduleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	at, err := service.ParsePublishAt(req.PublishAt)
	if err != nil {
		return err
	}
	v, err := a.svc.ScheduleRelease(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), req.DeployID, at)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toSchedule(v))
	return nil
}

func (a *api) scheduleUpload(w http.ResponseWriter, r *http.Request) error {
	var req scheduleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	at, err := service.ParsePublishAt(req.PublishAt)
	if err != nil {
		return err
	}
	v, err := a.svc.ScheduleUpload(r.Context(), actorFrom(r.Context()), r.PathValue("id"), at)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toSchedule(v))
	return nil
}

func (a *api) cancelSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return domain.ErrNotFound
	}
	v, err := a.svc.CancelSchedule(r.Context(), actorFrom(r.Context()), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSchedule(v))
	return nil
}

func (a *api) screenshot(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	shot, err := a.svc.Screenshot(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), q.Get("deploy_id"), q.Get("device"))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Dropsite-Site-Id", shot.SiteID)
	w.Header().Set("X-Dropsite-Deploy-Id", shot.DeployID)
	w.Header().Set("X-Dropsite-Device", shot.Device)
	w.Header().Set("X-Dropsite-Viewport", strconv.Itoa(shot.Width)+"x"+strconv.Itoa(shot.Height))
	w.Header().Set("Content-Length", strconv.Itoa(len(shot.PNG)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(shot.PNG)
	return nil
}

type visualDiffDTO struct {
	SiteID         string  `json:"site_id"`
	FromDeployID   string  `json:"from_deploy_id"`
	ToDeployID     string  `json:"to_deploy_id"`
	Device         string  `json:"device"`
	ChangedPercent float64 `json:"changed_percent"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	ImagePNG       string  `json:"image_png"`
}

func (a *api) visualDiff(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	d, err := a.svc.CompareReleases(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), q.Get("from"), q.Get("to"), q.Get("device"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, visualDiffDTO{
		SiteID: d.SiteID, FromDeployID: d.FromDeployID, ToDeployID: d.ToDeployID, Device: d.Device,
		ChangedPercent: d.ChangedPercent, Width: d.Width, Height: d.Height, ImagePNG: base64.StdEncoding.EncodeToString(d.PNG),
	})
	return nil
}
