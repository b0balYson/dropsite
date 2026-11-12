package remote

import (
	"context"
	"errors"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.WorkflowBackend = backend{}

// errorFor is toolError with the not-found text of the tool.
func (b backend) errorFor(err error, notFound string) error {
	e := domain.AsError(err)
	if err != nil && e.Kind == domain.KindNotFound {
		if e.Code == domain.CodeTemplateNotFound {
			return errors.New(mcp.NotFoundTemplate)
		}
		return errors.New(notFound)
	}
	return b.toolError(err)
}

func (b backend) CloneSite(ctx context.Context, ref, title, slug string) (mcp.DeployResult, error) {
	r, err := b.svc.CloneSite(ctx, b.actor, ref, title, slug, domain.SourceMCP)
	if err != nil {
		return mcp.DeployResult{}, b.toolError(err)
	}
	return toResult(r), nil
}

func (b backend) ListTemplates(_ context.Context, lang string) ([]mcp.Template, error) {
	ts := b.svc.ListTemplates(lang)
	out := make([]mcp.Template, 0, len(ts))
	for _, t := range ts {
		out = append(out, mcp.Template{Name: t.Name, Title: t.Title, Description: t.Description})
	}
	return out, nil
}

func (b backend) CreateSiteFromTemplate(ctx context.Context, name, title, slug, lang string) (mcp.DeployResult, error) {
	r, err := b.svc.CreateSiteFromTemplate(ctx, b.actor, name, title, slug, lang, domain.SourceMCP)
	if err != nil {
		return mcp.DeployResult{}, b.errorFor(err, mcp.NotFoundTemplate)
	}
	return toResult(r), nil
}

func toSchedule(v service.ScheduleView) mcp.Schedule {
	return mcp.Schedule{
		ID: strconv.FormatInt(v.ID, 10), SiteID: v.SiteID, SiteTitle: v.SiteTitle, SiteURL: v.SiteURL,
		DeployID: v.DeployID, PublishAt: stamp(&v.PublishAt), Status: string(v.Status), Error: v.Error,
		FinishedAt: stamp(v.FinishedAt), PreviewURL: v.PreviewURL,
	}
}

func (b backend) SchedulePublish(ctx context.Context, r mcp.ScheduleRequest) (mcp.Schedule, error) {
	at, err := service.ParsePublishAt(r.PublishAt)
	if err != nil {
		return mcp.Schedule{}, b.toolError(err)
	}
	var v service.ScheduleView
	if r.UploadID != "" {
		v, err = b.svc.ScheduleUpload(ctx, b.actor, r.UploadID, at)
	} else {
		v, err = b.svc.ScheduleRelease(ctx, b.actor, r.Site, r.DeployID, at)
	}
	if err != nil {
		return mcp.Schedule{}, b.toolError(err)
	}
	return toSchedule(v), nil
}

func (b backend) ListSchedules(ctx context.Context, site string) ([]mcp.Schedule, error) {
	rows, err := b.svc.ListSchedules(ctx, b.actor, site)
	if err != nil {
		return nil, b.toolError(err)
	}
	out := make([]mcp.Schedule, 0, len(rows))
	for _, v := range rows {
		out = append(out, toSchedule(v))
	}
	return out, nil
}

func (b backend) CancelSchedule(ctx context.Context, id string) (mcp.Schedule, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return mcp.Schedule{}, errors.New(mcp.NotFoundSchedule)
	}
	v, err := b.svc.CancelSchedule(ctx, b.actor, n)
	if err != nil {
		return mcp.Schedule{}, b.errorFor(err, mcp.NotFoundSchedule)
	}
	return toSchedule(v), nil
}

func (b backend) Screenshot(ctx context.Context, site, deployID, device string) (mcp.Screenshot, error) {
	s, err := b.svc.Screenshot(ctx, b.actor, site, deployID, device)
	if err != nil {
		return mcp.Screenshot{}, b.toolError(err)
	}
	return mcp.Screenshot{SiteID: s.SiteID, DeployID: s.DeployID, Device: s.Device, Width: s.Width, Height: s.Height, PNG: s.PNG}, nil
}

func (b backend) CompareVersions(ctx context.Context, site, from, to, device string) (mcp.VisualDiff, error) {
	d, err := b.svc.CompareReleases(ctx, b.actor, site, from, to, device)
	if err != nil {
		return mcp.VisualDiff{}, b.toolError(err)
	}
	return mcp.VisualDiff{
		SiteID: d.SiteID, FromDeployID: d.FromDeployID, ToDeployID: d.ToDeployID, Device: d.Device,
		ChangedPercent: d.ChangedPercent, Width: d.Width, Height: d.Height, PNG: d.PNG,
	}, nil
}
