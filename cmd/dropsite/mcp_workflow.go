package main

import (
	"context"
	"errors"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Copies, templates, scheduled publishes and screenshots of the local
// server, through the API.

var _ mcp.WorkflowBackend = localBackend{}

// workflowError is toolErrorFor that keeps the renderer's "not available"
// answer (HTTP 503) as the coded error the remote server returns.
func workflowError(err error, notFound string) error {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Code == domain.CodeRendererUnavailable {
		return mcp.CodedError(ae.Message, ae.Code, ae.Details)
	}
	return toolErrorFor(err, notFound)
}

func (b localBackend) CloneSite(ctx context.Context, ref, title, slug string) (mcp.DeployResult, error) {
	r, err := b.c.CloneSite(ctx, ref, client.NewSiteRequest{Title: title, Slug: slug})
	if err != nil {
		return mcp.DeployResult{}, toolError(err)
	}
	return fromResult(r), nil
}

func (b localBackend) ListTemplates(ctx context.Context, lang string) ([]mcp.Template, error) {
	l, err := b.c.ListTemplates(ctx, lang)
	if err != nil {
		return nil, toolError(err)
	}
	out := make([]mcp.Template, 0, len(l.Items))
	for _, t := range l.Items {
		out = append(out, mcp.Template(t))
	}
	return out, nil
}

func (b localBackend) CreateSiteFromTemplate(ctx context.Context, name, title, slug, lang string) (mcp.DeployResult, error) {
	r, err := b.c.CreateSiteFromTemplate(ctx, name, client.NewSiteRequest{Title: title, Slug: slug, Lang: lang})
	if err != nil {
		return mcp.DeployResult{}, toolErrorFor(err, mcp.NotFoundTemplate)
	}
	return fromResult(r), nil
}

func fromSchedule(s *client.Schedule) mcp.Schedule {
	return mcp.Schedule{
		ID: s.ID, SiteID: s.SiteID, SiteTitle: s.SiteTitle, SiteURL: s.SiteURL, DeployID: s.DeployID,
		PublishAt: stampTime(&s.PublishAt), Status: s.Status, Error: s.Error, FinishedAt: stampTime(s.FinishedAt),
		PreviewURL: deref(s.PreviewURL),
	}
}

func (b localBackend) SchedulePublish(ctx context.Context, r mcp.ScheduleRequest) (mcp.Schedule, error) {
	var (
		s   *client.Schedule
		err error
	)
	if r.UploadID != "" {
		s, err = b.c.ScheduleUpload(ctx, r.UploadID, r.PublishAt)
	} else {
		s, err = b.c.ScheduleRelease(ctx, r.Site, r.DeployID, r.PublishAt)
	}
	if err != nil {
		return mcp.Schedule{}, toolError(err)
	}
	return fromSchedule(s), nil
}

func (b localBackend) ListSchedules(ctx context.Context, site string) ([]mcp.Schedule, error) {
	l, err := b.c.ListSchedules(ctx, site)
	if err != nil {
		return nil, toolError(err)
	}
	out := make([]mcp.Schedule, 0, len(l.Items))
	for i := range l.Items {
		out = append(out, fromSchedule(&l.Items[i]))
	}
	return out, nil
}

func (b localBackend) CancelSchedule(ctx context.Context, id string) (mcp.Schedule, error) {
	s, err := b.c.CancelSchedule(ctx, id)
	if err != nil {
		return mcp.Schedule{}, toolErrorFor(err, mcp.NotFoundSchedule)
	}
	return fromSchedule(s), nil
}

func (b localBackend) Screenshot(ctx context.Context, site, deployID, device string) (mcp.Screenshot, error) {
	s, err := b.c.Screenshot(ctx, site, deployID, device)
	if err != nil {
		return mcp.Screenshot{}, workflowError(err, mcp.NotFoundSite)
	}
	return mcp.Screenshot{
		SiteID: s.SiteID, DeployID: s.DeployID, Device: s.Device, Width: s.Width, Height: s.Height, PNG: s.PNG,
	}, nil
}

func (b localBackend) CompareVersions(ctx context.Context, site, from, to, device string) (mcp.VisualDiff, error) {
	d, err := b.c.CompareVersions(ctx, site, from, to, device)
	if err != nil {
		return mcp.VisualDiff{}, workflowError(err, mcp.NotFoundSite)
	}
	img, err := d.PNG()
	if err != nil {
		return mcp.VisualDiff{}, toolError(err)
	}
	return mcp.VisualDiff{
		SiteID: d.SiteID, FromDeployID: d.FromDeployID, ToDeployID: d.ToDeployID, Device: d.Device,
		ChangedPercent: d.ChangedPercent, Width: d.Width, Height: d.Height, PNG: img,
	}, nil
}
