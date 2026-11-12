package client

import (
	"context"
	"net/http"
)

// PublishSettings are a site's publish options.
type PublishSettings struct {
	SiteID              string `json:"site_id"`
	Optimize            bool   `json:"optimize"`
	InstallationEnabled bool   `json:"installation_enabled"`
	Effective           bool   `json:"effective"`
}

func publishSettingsPath(ref string) string { return sitePath(ref) + "/publish-settings" }

// PublishSettings returns a site's publish options.
func (c *Client) PublishSettings(ctx context.Context, ref string) (*PublishSettings, error) {
	var out PublishSettings
	if err := c.doJSON(ctx, http.MethodGet, publishSettingsPath(ref), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetOptimize turns publish-time optimization of a site on or off.
func (c *Client) SetOptimize(ctx context.Context, ref string, on bool) (*PublishSettings, error) {
	var out PublishSettings
	body := map[string]bool{"optimize": on}
	if err := c.doJSON(ctx, http.MethodPatch, publishSettingsPath(ref), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
