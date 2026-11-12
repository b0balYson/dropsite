package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

var _ mcp.SiteAttacher = localBackend{}

// SiteAttachments returns what is attached to a site for get_site.
func (b localBackend) SiteAttachments(ctx context.Context, ref string) (mcp.SiteAttached, error) {
	at, err := b.c.SiteAttachments(ctx, ref)
	if err != nil {
		return mcp.SiteAttached{}, toolError(err)
	}
	cols := make([]mcp.AttachedCollection, 0, len(at.Collections))
	for _, c := range at.Collections {
		cols = append(cols, mcp.AttachedCollection(c))
	}
	return mcp.NewSiteAttached(at.Forms.Names, at.Forms.Channels, at.Forms.WorkingChannels, at.Forms.RequireAltcha,
		mcp.CounterIDs(at.Counters.Own), mcp.CounterIDs(at.Counters.Global)).WithData(cols, at.ProxyEndpoints), nil
}
