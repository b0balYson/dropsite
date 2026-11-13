package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Draft releases on the local MCP server (change staged-publish), through
// the HTTP API like every other tool.

func fromDraft(d *client.Draft) *mcp.Draft {
	if d == nil {
		return nil
	}
	return &mcp.Draft{DeployID: d.DeployID, CreatedAt: stampTime(&d.CreatedAt), PreviewURL: deref(d.PreviewURL)}
}

func draftPreview(d *client.Draft) string {
	if d == nil {
		return ""
	}
	return deref(d.PreviewURL)
}

func (b localBackend) PublishDraft(ctx context.Context, ref string) (mcp.DeployResult, error) {
	r, err := b.c.PublishDraft(ctx, ref)
	if err != nil {
		return mcp.DeployResult{}, toolError(err)
	}
	return fromResult(r), nil
}

func (b localBackend) DiscardDraft(ctx context.Context, ref string) (mcp.Site, error) {
	s, err := b.c.DiscardDraft(ctx, ref)
	if err != nil {
		return mcp.Site{}, toolError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) CommitUploadDraft(ctx context.Context, id string, deletes []string) (mcp.FilesResult, error) {
	r, err := b.c.CommitUploadWith(ctx, id, deletes, true)
	if err != nil {
		return mcp.FilesResult{}, toolError(err)
	}
	return filesResult(r, "Draft saved"), nil
}

var _ mcp.DraftBackend = localBackend{}
