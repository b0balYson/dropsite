package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Site file tools of the local server, through the API. The server checks
// access, sizes and the base release, and guards downloads against SSRF.

var _ mcp.SiteFilesBackend = localBackend{}

func (b localBackend) ListSiteFiles(ctx context.Context, ref, deployID, prefix string) (mcp.SiteFileList, error) {
	l, err := b.c.ListSiteFiles(ctx, ref, deployID, prefix)
	if err != nil {
		return mcp.SiteFileList{}, toolError(err)
	}
	out := mcp.SiteFileList{
		SiteID: l.SiteID, URL: l.URL, DeployID: l.DeployID, Current: l.Current,
		Files: make([]mcp.SiteFile, 0, len(l.Files)), TotalFiles: l.TotalFiles, TotalBytes: l.TotalBytes, Truncated: l.Truncated,
	}
	for _, f := range l.Files {
		out.Files = append(out.Files, mcp.SiteFile(f))
	}
	return out, nil
}

func (b localBackend) ReadSiteFiles(ctx context.Context, ref, deployID string, paths []string) (mcp.SiteFileRead, error) {
	r, err := b.c.ReadSiteFiles(ctx, ref, deployID, paths)
	if err != nil {
		return mcp.SiteFileRead{}, toolError(err)
	}
	missing := r.Missing
	if len(missing) == 0 {
		missing = nil
	}
	out := mcp.SiteFileRead{
		SiteID: r.SiteID, DeployID: r.DeployID, Current: r.Current, Missing: missing, Truncated: r.Truncated,
		Files: make([]mcp.SiteFileContent, 0, len(r.Files)),
	}
	for _, f := range r.Files {
		out.Files = append(out.Files, mcp.SiteFileContent{Path: f.Path, Size: f.Size, SHA256: f.SHA256, Type: f.Type,
			Encoding: f.Encoding, Content: f.Content, Note: f.Note})
	}
	return out, nil
}

func toInlineFiles(files []mcp.File) []client.InlineFile {
	out := make([]client.InlineFile, 0, len(files))
	for _, f := range files {
		out = append(out, client.InlineFile(f))
	}
	return out
}

func toFetchSpecs(fs []mcp.FetchFile) []client.FetchSpec {
	out := make([]client.FetchSpec, 0, len(fs))
	for _, f := range fs {
		out = append(out, client.FetchSpec(f))
	}
	return out
}

func fromFetched(fs []client.FetchedFile) []mcp.FetchedFile {
	if len(fs) == 0 {
		return nil
	}
	out := make([]mcp.FetchedFile, 0, len(fs))
	for _, f := range fs {
		out = append(out, mcp.FetchedFile{URL: f.URL, Path: f.Path, Type: f.Type, Size: f.Bytes,
			OriginalSize: f.OriginalBytes, Resized: f.Resized})
	}
	return out
}

func filesResult(r *client.FilesResult, verb string) mcp.FilesResult {
	return mcp.NewFilesResult(mcp.FilesOutcome{
		SiteID: r.Site.ID, DeployID: r.DeployID, Base: r.BaseDeployID, URL: r.URL, URLs: r.URLs,
		Written: r.Written, Deleted: r.Deleted, FileCount: r.Deploy.FileCount, Fetched: fromFetched(r.Fetched),
		Quality: r.Quality, Draft: r.Deploy.Draft, PreviewURL: draftPreview(r.Site.Draft),
	}, verb)
}

func (b localBackend) UpdateSiteFiles(ctx context.Context, c mcp.FilesChange) (mcp.FilesResult, error) {
	r, err := b.c.UpdateSiteFiles(ctx, c.Site, client.FilesChange{
		BaseDeployID: c.Base, Files: toInlineFiles(c.Files), Delete: c.Delete, Fetch: toFetchSpecs(c.Fetch),
		Draft: c.Draft,
	})
	if err != nil {
		return mcp.FilesResult{}, toolError(err)
	}
	return filesResult(r, "Site updated"), nil
}

func fromUpload(u *client.Upload) mcp.Upload {
	return mcp.Upload{
		UploadID: u.ID, Mode: u.Mode, SiteID: u.SiteID, BaseDeployID: u.BaseDeployID, Files: u.Files, Bytes: u.Bytes,
		ExpiresAt: stampTime(&u.ExpiresAt), MaxBytes: u.MaxBytes, MaxFiles: u.MaxFiles, MaxFileBytes: u.MaxFileBytes,
		MaxCallBytes: u.MaxCallBytes, Fetched: fromFetched(u.Fetched), UploadURL: u.UploadURL,
		UploadURLExpiresAt: stampTime(u.UploadURLExpiresAt),
	}
}

func (b localBackend) BeginUpload(ctx context.Context, u mcp.UploadStart) (mcp.Upload, error) {
	r, err := b.c.BeginUpload(ctx, client.UploadStart{Site: u.Site, Title: u.Title, Mode: u.Mode, BaseDeployID: u.Base})
	if err != nil {
		return mcp.Upload{}, toolError(err)
	}
	return fromUpload(r), nil
}

func (b localBackend) UploadFiles(ctx context.Context, id string, files []mcp.File, fetch []mcp.FetchFile) (mcp.Upload, error) {
	r, err := b.c.UploadFiles(ctx, id, toInlineFiles(files), toFetchSpecs(fetch))
	if err != nil {
		return mcp.Upload{}, toolError(err)
	}
	return fromUpload(r), nil
}

func (b localBackend) CommitUpload(ctx context.Context, id string, deletes []string) (mcp.FilesResult, error) {
	r, err := b.c.CommitUpload(ctx, id, deletes)
	if err != nil {
		return mcp.FilesResult{}, toolError(err)
	}
	return filesResult(r, "Site published"), nil
}
