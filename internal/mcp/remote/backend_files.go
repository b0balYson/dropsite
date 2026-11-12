package remote

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/quality"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.SiteFilesBackend = backend{}

func toInline(files []mcp.File) []service.InlineFile {
	out := make([]service.InlineFile, 0, len(files))
	for _, f := range files {
		out = append(out, service.InlineFile{Path: f.Path, Content: f.Content, Encoding: f.Encoding})
	}
	return out
}

func toFetch(fs []mcp.FetchFile) []service.FetchSpec {
	out := make([]service.FetchSpec, 0, len(fs))
	for _, f := range fs {
		out = append(out, service.FetchSpec{URL: f.URL, Path: f.Path})
	}
	return out
}

func toFetched(fs []service.FetchedFile) []mcp.FetchedFile {
	if len(fs) == 0 {
		return nil
	}
	out := make([]mcp.FetchedFile, 0, len(fs))
	for _, f := range fs {
		out = append(out, mcp.FetchedFile{
			URL: f.URL, Path: f.Path, Type: f.Type, Size: f.Bytes, OriginalSize: f.OriginalBytes, Resized: f.Resized,
		})
	}
	return out
}

func (b backend) ListSiteFiles(ctx context.Context, ref, deployID, prefix string) (mcp.SiteFileList, error) {
	l, err := b.svc.ListSiteFiles(ctx, b.actor, ref, deployID, prefix)
	if err != nil {
		return mcp.SiteFileList{}, b.toolError(err)
	}
	out := mcp.SiteFileList{
		SiteID: l.Site.Site.ID, URL: l.Site.URL, DeployID: l.Deploy.ID, Current: l.Current,
		Files: make([]mcp.SiteFile, 0, len(l.Files)), TotalFiles: l.TotalFiles, TotalBytes: l.TotalBytes, Truncated: l.Truncated,
	}
	for _, f := range l.Files {
		out.Files = append(out.Files, mcp.SiteFile(f))
	}
	return out, nil
}

func (b backend) ReadSiteFiles(ctx context.Context, ref, deployID string, paths []string) (mcp.SiteFileRead, error) {
	r, err := b.svc.ReadSiteFiles(ctx, b.actor, ref, deployID, paths)
	if err != nil {
		return mcp.SiteFileRead{}, b.toolError(err)
	}
	out := mcp.SiteFileRead{
		SiteID: r.Site.Site.ID, DeployID: r.Deploy.ID, Current: r.Current, Missing: r.Missing, Truncated: r.Truncated,
		Files: make([]mcp.SiteFileContent, 0, len(r.Files)),
	}
	for _, f := range r.Files {
		c := mcp.SiteFileContent{Path: f.Path, Size: f.Size, SHA256: f.SHA256, Type: f.Type, Note: f.Note}
		switch {
		case f.Content == nil:
		case f.Text:
			c.Encoding, c.Content = "utf8", string(f.Content)
		default:
			c.Encoding, c.Content = "base64", base64.StdEncoding.EncodeToString(f.Content)
		}
		out.Files = append(out.Files, c)
	}
	return out, nil
}

func filesResult(r service.UpdateResult, verb string) mcp.FilesResult {
	return mcp.NewFilesResult(mcp.FilesOutcome{
		SiteID: r.Site.Site.ID, DeployID: r.Deploy.ID, Base: r.Base, URL: r.Site.URL, URLs: r.Site.URLs,
		Written: r.Written, Deleted: r.Deleted, FileCount: r.Deploy.FileCount, Fetched: toFetched(r.Fetched),
		Quality: quality.Summarize(r.Quality), Draft: isDraft(r.Site, r.Deploy.ID), PreviewURL: draftPreview(r.Site),
	}, verb)
}

func (b backend) UpdateSiteFiles(ctx context.Context, c mcp.FilesChange) (mcp.FilesResult, error) {
	r, err := b.svc.UpdateSiteFilesWith(ctx, b.actor, c.Site, c.Base, service.FileChanges{
		Put: toInline(c.Files), Delete: c.Delete, Fetch: toFetch(c.Fetch),
	}, service.PublishOptions{Draft: c.Draft})
	if err != nil {
		return mcp.FilesResult{}, b.toolError(err)
	}
	return filesResult(r, "Site updated"), nil
}

func toUpload(i service.UploadInfo, fetched []service.FetchedFile) mcp.Upload {
	return mcp.Upload{
		UploadID: i.ID, Mode: i.Mode, SiteID: i.SiteID, BaseDeployID: i.Base, Files: i.Files, Bytes: i.Bytes,
		ExpiresAt: i.ExpiresAt.UTC().Format(time.RFC3339), MaxBytes: i.MaxBytes, MaxFiles: i.MaxFiles,
		MaxFileBytes: i.MaxFileBytes, MaxCallBytes: i.MaxCallBytes, Fetched: toFetched(fetched),
	}
}

func (b backend) BeginUpload(ctx context.Context, u mcp.UploadStart) (mcp.Upload, error) {
	i, err := b.svc.BeginUpload(ctx, b.actor, u.Site, u.Title, u.Mode, u.Base)
	if err != nil {
		return mcp.Upload{}, b.toolError(err)
	}
	return toUpload(i, nil), nil
}

func (b backend) UploadFiles(ctx context.Context, id string, files []mcp.File, fetch []mcp.FetchFile) (mcp.Upload, error) {
	i, fetched, err := b.svc.UploadFiles(ctx, b.actor, id, toInline(files), toFetch(fetch))
	if err != nil {
		return mcp.Upload{}, b.toolError(err)
	}
	return toUpload(i, fetched), nil
}

func (b backend) CommitUpload(ctx context.Context, id string, deletes []string) (mcp.FilesResult, error) {
	r, err := b.svc.CommitUpload(ctx, b.actor, id, deletes)
	if err != nil {
		return mcp.FilesResult{}, b.toolError(err)
	}
	return filesResult(r, "Site published"), nil
}
