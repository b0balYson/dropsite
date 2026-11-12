package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PublishSettings are a site's publish options as tools return them.
type PublishSettings struct {
	SiteID              string `json:"site_id"`
	Optimize            bool   `json:"optimize" jsonschema:"the site's own setting: optimize new versions at publish time"`
	InstallationEnabled bool   `json:"installation_enabled" jsonschema:"false when the server administrator turned optimization off for everyone"`
	Effective           bool   `json:"effective" jsonschema:"true when the next publication will be optimized"`
	Summary             string `json:"summary" jsonschema:"short human summary to relay to the user"`
}

// PublishBackend reads and changes publish settings. The tools fail when the
// backend does not implement it.
type PublishBackend interface {
	GetPublishSettings(ctx context.Context, ref string) (PublishSettings, error)
	SetPublishSettings(ctx context.Context, ref string, optimize bool) (PublishSettings, error)
}

type publishSettingsArgs struct {
	Site     string `json:"site" jsonschema:"site ID or slug"`
	Optimize bool   `json:"optimize" jsonschema:"true to optimize new versions (default), false to publish files exactly as uploaded"`
}

var errNoPublish = errors.New("this server cannot change publish settings; use the web interface of dropsite")

// optimizeExplained is shared by both publish settings tools.
const optimizeExplained = "Optimization never changes the site's files: at each publication the server adds " +
	"lighter WebP copies of JPEG and PNG images (served only to browsers that support WebP) and a robots.txt, " +
	"sitemap.xml and favicon.ico when the site has none (no robots.txt or sitemap.xml for sites hidden from " +
	"search). It is on by default."

func (t tools) registerPublish(s *sdk.Server, o Options) {
	if !o.Cabinet {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "get_publish_settings", Annotations: readOnly("Publish settings"),
		Description: "Show whether new versions of a site are optimized at publish time. " + optimizeExplained},
		t.getPublishSettings)
	sdk.AddTool(s, &sdk.Tool{Name: "set_publish_settings", Annotations: writes("Change publish settings", false, true),
		Description: "Turn publish-time optimization of a site on or off; it applies from the next publication. " +
			optimizeExplained + " Turn it off only when the user asks, e.g. when they want exactly their own " +
			"files served."}, t.setPublishSettings)
}

func (t tools) publish(ctx context.Context, req *sdk.CallToolRequest) (PublishBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	p, ok := b.(PublishBackend)
	if !ok {
		return nil, errNoPublish
	}
	return p, nil
}

func (t tools) getPublishSettings(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, PublishSettings, error) {
	b, err := t.publish(ctx, req)
	if err != nil {
		return nil, PublishSettings{}, err
	}
	p, err := b.GetPublishSettings(ctx, in.Site)
	return nil, p, err
}

func (t tools) setPublishSettings(ctx context.Context, req *sdk.CallToolRequest, in publishSettingsArgs) (*sdk.CallToolResult, PublishSettings, error) {
	b, err := t.publish(ctx, req)
	if err != nil {
		return nil, PublishSettings{}, err
	}
	p, err := b.SetPublishSettings(ctx, in.Site, in.Optimize)
	return nil, p, err
}

// NewPublishSettings builds the tool result with its summary; both servers
// use it so their answers match.
func NewPublishSettings(siteID string, optimize, installation, effective bool) PublishSettings {
	p := PublishSettings{SiteID: siteID, Optimize: optimize, InstallationEnabled: installation, Effective: effective}
	switch {
	case !installation:
		p.Summary = "Optimization is turned off on this server by its administrator; new versions are published as uploaded."
	case optimize:
		p.Summary = "New versions are optimized: WebP copies of images and missing robots.txt, sitemap.xml and favicon.ico are added; the site's files stay unchanged."
	default:
		p.Summary = "Optimization is off for this site: new versions are published exactly as uploaded."
	}
	return p
}
