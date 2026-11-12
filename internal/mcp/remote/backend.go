// Package remote serves the MCP tools on the API host (POST /mcp) for one
// authenticated user per request, backed by internal/service, which makes
// every access decision.
package remote

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

// backend runs tools as one actor.
type backend struct {
	svc   *service.Service
	actor service.Actor
	log   *slog.Logger
}

func stamp(t *time.Time) string { return mcp.Stamp(t) }

func toDeploy(d domain.Deploy, current, preview string) mcp.Deploy {
	return mcp.Deploy{
		ID: d.ID, Source: string(d.Source), SizeBytes: d.SizeBytes, FileCount: d.FileCount,
		Status: string(d.Status), CreatedAt: stamp(&d.CreatedAt), Current: d.ID == current, PreviewURL: preview,
	}
}

func toSite(v service.SiteView) mcp.Site {
	s := mcp.Site{
		ID: v.Site.ID, Title: v.Site.Title, URL: v.URL, URLs: v.URLs, Slug: v.Slug, UpdatedAt: stamp(&v.Site.UpdatedAt),
		SiteExtras: siteExtras(v.Site, v.Hostnames),
	}
	if v.Current != nil {
		d := toDeploy(*v.Current, v.Current.ID, v.CurrentPreviewURL)
		s.CurrentDeploy = &d
	}
	return s
}

func toResult(r service.DeployResult) mcp.DeployResult {
	return mcp.DeployResult{SiteID: r.Site.Site.ID, DeployID: r.Deploy.ID, URL: r.Site.URL, URLs: r.Site.URLs}
}

// toolError turns a service error into text the model can act on. Internal
// details never reach the client.
func (b backend) toolError(err error) error {
	if err == nil {
		return nil
	}
	e := domain.AsError(err)
	switch e.Kind {
	case domain.KindInternal:
		b.log.Error("mcp tool failed", "user_id", b.actor.User.ID, "err", err)
		return errors.New(mcp.InternalErrorText)
	case domain.KindNotFound:
		return errors.New(mcp.NotFoundSite)
	case domain.KindRateLimited:
		return mcp.RateLimitedError(int64(math.Ceil(e.RetryAfter.Seconds())))
	case domain.KindUnauthorized:
		return errors.New("the connection to dropsite is no longer authorized; reconnect it")
	}
	return mcp.CodedError(e.Message, e.Code, e.Params)
}

func (b backend) ListSites(ctx context.Context) ([]mcp.Site, error) {
	rows, _, err := b.svc.ListSites(ctx, b.actor, service.Page{Limit: service.MaxPageSize})
	if err != nil {
		return nil, b.toolError(err)
	}
	out := make([]mcp.Site, 0, len(rows))
	for _, r := range rows {
		out = append(out, mcp.Site{
			ID: r.ID, Title: r.Title, URL: r.URL, URLs: r.URLs, Slug: r.Slug,
			LastDeployAt: stamp(r.LastDeployAt), UpdatedAt: stamp(&r.UpdatedAt),
			SiteExtras: siteExtras(r.Site, r.Hostnames),
		})
	}
	return out, nil
}

func (b backend) CreateSite(ctx context.Context, title, slug string) (mcp.Site, error) {
	v, err := b.svc.CreateSite(ctx, b.actor, title, slug)
	if err != nil {
		return mcp.Site{}, b.toolError(err)
	}
	return toSite(v), nil
}

func (b backend) GetSite(ctx context.Context, ref string) (mcp.Site, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.Site{}, b.toolError(err)
	}
	return toSite(v), nil
}

func (b backend) SetSlug(ctx context.Context, ref, slug string) (mcp.Site, error) {
	v, err := b.svc.UpdateSite(ctx, b.actor, ref, service.SiteUpdate{Slug: &slug})
	if err != nil {
		return mcp.Site{}, b.toolError(err)
	}
	return toSite(v), nil
}

func (b backend) ListDeploys(ctx context.Context, ref string) ([]mcp.Deploy, error) {
	ds, v, err := b.svc.ListDeploys(ctx, b.actor, ref)
	if err != nil {
		return nil, b.toolError(err)
	}
	current := ""
	if v.Site.CurrentDeployID != nil {
		current = *v.Site.CurrentDeployID
	}
	out := make([]mcp.Deploy, 0, len(ds))
	for _, d := range ds {
		out = append(out, toDeploy(d, current, b.svc.PreviewURL(d)))
	}
	return out, nil
}

func (b backend) Rollback(ctx context.Context, ref, deployID string) (mcp.DeployResult, error) {
	r, err := b.svc.Rollback(ctx, b.actor, ref, deployID)
	if err != nil {
		return mcp.DeployResult{}, b.toolError(err)
	}
	return toResult(r), nil
}

func (b backend) DeleteSite(ctx context.Context, ref string) error {
	return b.toolError(b.svc.DeleteSite(ctx, b.actor, ref))
}

func (b backend) DeployFiles(ctx context.Context, ref, title string, files []mcp.File) (mcp.DeployResult, error) {
	in := make([]service.InlineFile, 0, len(files))
	for _, f := range files {
		in = append(in, service.InlineFile{Path: f.Path, Content: f.Content, Encoding: f.Encoding})
	}
	r, err := b.svc.DeployFiles(ctx, b.actor, ref, title, in)
	if err != nil {
		return mcp.DeployResult{}, b.toolError(err)
	}
	return toResult(r), nil
}
