package remote

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

var _ mcp.SiteAttacher = backend{}

// SiteAttachments returns what is attached to a site for get_site.
func (b backend) SiteAttachments(ctx context.Context, ref string) (mcp.SiteAttached, error) {
	at, err := b.svc.SiteAttachments(ctx, b.actor, ref)
	if err != nil {
		return mcp.SiteAttached{}, b.toolError(err)
	}
	cols := make([]mcp.AttachedCollection, 0, len(at.Collections))
	for _, c := range at.Collections {
		cols = append(cols, mcp.AttachedCollection{Name: c.Name, Mode: string(c.Mode), Documents: c.Documents,
			Pending: c.Pending})
	}
	return mcp.NewSiteAttached(at.FormNames, at.Channels, at.WorkingChannels, at.RequireAltcha,
		counterIDs(at.Counters.Own), counterIDs(at.Counters.Global)).WithData(cols, at.ProxyEndpoints), nil
}
