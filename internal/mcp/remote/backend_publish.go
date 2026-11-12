package remote

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.PublishBackend = backend{}

func toPublishSettings(v service.PublishSettingsView) mcp.PublishSettings {
	return mcp.NewPublishSettings(v.SiteID, v.Optimize, v.InstallationEnabled, v.Effective)
}

func (b backend) GetPublishSettings(ctx context.Context, ref string) (mcp.PublishSettings, error) {
	v, err := b.svc.GetPublishSettings(ctx, b.actor, ref)
	if err != nil {
		return mcp.PublishSettings{}, b.toolError(err)
	}
	return toPublishSettings(v), nil
}

func (b backend) SetPublishSettings(ctx context.Context, ref string, optimize bool) (mcp.PublishSettings, error) {
	v, err := b.svc.SetPublishSettings(ctx, b.actor, ref, optimize)
	if err != nil {
		return mcp.PublishSettings{}, b.toolError(err)
	}
	return toPublishSettings(v), nil
}
