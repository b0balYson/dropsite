package remote

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

// Draft releases (change staged-publish).

func toDraft(v service.SiteView) *mcp.Draft {
	if v.Draft == nil {
		return nil
	}
	return &mcp.Draft{DeployID: v.Draft.Deploy.ID, CreatedAt: stamp(&v.Draft.CreatedAt), PreviewURL: v.Draft.PreviewURL}
}

// isDraft reports whether deployID is the site's draft.
func isDraft(v service.SiteView, deployID string) bool {
	return v.Draft != nil && v.Draft.Deploy.ID == deployID
}

func draftPreview(v service.SiteView) string {
	if v.Draft == nil {
		return ""
	}
	return v.Draft.PreviewURL
}

func (b backend) PublishDraft(ctx context.Context, ref string) (mcp.DeployResult, error) {
	r, err := b.svc.PublishDraft(ctx, b.actor, ref)
	if err != nil {
		return mcp.DeployResult{}, b.toolError(err)
	}
	return toResult(r), nil
}

func (b backend) DiscardDraft(ctx context.Context, ref string) (mcp.Site, error) {
	v, err := b.svc.DiscardDraft(ctx, b.actor, ref)
	if err != nil {
		return mcp.Site{}, b.toolError(err)
	}
	return toSite(v), nil
}

func (b backend) CommitUploadDraft(ctx context.Context, id string, deletes []string) (mcp.FilesResult, error) {
	r, err := b.svc.CommitUploadWith(ctx, b.actor, id, deletes, service.PublishOptions{Draft: true})
	if err != nil {
		return mcp.FilesResult{}, b.toolError(err)
	}
	return filesResult(r, "Draft saved"), nil
}

func (b backend) DeployFilesDraft(ctx context.Context, ref, title string, files []mcp.File) (mcp.DeployResult, error) {
	in := make([]service.InlineFile, 0, len(files))
	for _, f := range files {
		in = append(in, service.InlineFile{Path: f.Path, Content: f.Content, Encoding: f.Encoding})
	}
	r, err := b.svc.DeployFilesWith(ctx, b.actor, ref, title, in, service.PublishOptions{Draft: true})
	if err != nil {
		return mcp.DeployResult{}, b.toolError(err)
	}
	return toResult(r), nil
}

var (
	_ mcp.DraftBackend       = backend{}
	_ mcp.DraftFilesDeployer = backend{}
)
