package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Publish settings tools of the local server, through the API.

var _ mcp.PublishBackend = localBackend{}

func fromPublishSettings(p *client.PublishSettings) mcp.PublishSettings {
	return mcp.NewPublishSettings(p.SiteID, p.Optimize, p.InstallationEnabled, p.Effective)
}

func (b localBackend) GetPublishSettings(ctx context.Context, ref string) (mcp.PublishSettings, error) {
	p, err := b.c.PublishSettings(ctx, ref)
	if err != nil {
		return mcp.PublishSettings{}, toolError(err)
	}
	return fromPublishSettings(p), nil
}

func (b localBackend) SetPublishSettings(ctx context.Context, ref string, optimize bool) (mcp.PublishSettings, error) {
	p, err := b.c.SetOptimize(ctx, ref, optimize)
	if err != nil {
		return mcp.PublishSettings{}, toolError(err)
	}
	return fromPublishSettings(p), nil
}
