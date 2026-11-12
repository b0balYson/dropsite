package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type listFilesArgs struct {
	Site     string `json:"site" jsonschema:"site ID or slug"`
	DeployID string `json:"deploy_id,omitempty" jsonschema:"release to list; empty means the release being served"`
	Prefix   string `json:"prefix,omitempty" jsonschema:"only paths starting with this, e.g. img/"`
}

type readFilesArgs struct {
	Site     string   `json:"site" jsonschema:"site ID or slug"`
	DeployID string   `json:"deploy_id,omitempty" jsonschema:"release to read; empty means the release being served"`
	Paths    []string `json:"paths" jsonschema:"up to 50 file paths or patterns such as index.html or css/*.css"`
}

type updateFilesArgs struct {
	Site         string      `json:"site" jsonschema:"site ID or slug"`
	BaseDeployID string      `json:"base_deploy_id" jsonschema:"deploy_id returned by list_site_files or read_site_files for the files you changed"`
	Files        []File      `json:"files,omitempty" jsonschema:"files to add or replace, whole content each; at most 1 MB in total"`
	Delete       []string    `json:"delete,omitempty" jsonschema:"files or folders to remove, e.g. old.html or img/old/"`
	Fetch        []FetchFile `json:"fetch,omitempty" jsonschema:"images, fonts, stylesheets or scripts the server downloads from public https addresses (up to 20)"`
}

type beginUploadArgs struct {
	Site         string `json:"site,omitempty" jsonschema:"site ID or slug; omit to create a new site when committing"`
	Title        string `json:"title,omitempty" jsonschema:"name of the new site when site is omitted"`
	Mode         string `json:"mode,omitempty" jsonschema:"replace (default): the site becomes exactly the uploaded files; patch: uploaded files are added on top of base_deploy_id"`
	BaseDeployID string `json:"base_deploy_id,omitempty" jsonschema:"required for patch: deploy_id from list_site_files"`
}

type uploadFilesArgs struct {
	UploadID string      `json:"upload_id" jsonschema:"from begin_upload"`
	Files    []File      `json:"files,omitempty" jsonschema:"next part of the files; at most 1 MB of content per call"`
	Fetch    []FetchFile `json:"fetch,omitempty" jsonschema:"files the server downloads from public https addresses (up to 20)"`
}

type commitUploadArgs struct {
	UploadID string   `json:"upload_id" jsonschema:"from begin_upload"`
	Delete   []string `json:"delete,omitempty" jsonschema:"patch mode only: files or folders of the base release to remove"`
}

var errNoSiteFiles = errors.New("this server cannot read or edit site files")

// fetchAdvice is shared advice for the tools that write files.
const fetchAdvice = " Put images, fonts, CSS and JS inside the site (fetch downloads them from public https " +
	"addresses; large photos are scaled down to 2000 px wide) instead of linking to other servers, so the site " +
	"stays fast and keeps working."

// registerFiles adds the tools for reading and editing site files
// (o.SiteFiles); the backend must implement SiteFilesBackend.
func (t tools) registerFiles(s *sdk.Server, o Options) {
	if !o.SiteFiles {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "list_site_files", Annotations: readOnly("List site files"),
		Description: "List the files of a published site (the version being served, or deploy_id). Use it before " +
			"changing an existing site, e.g. when the user asks to change prices, texts or photos. Returns deploy_id: " +
			"pass it as base_deploy_id to update_site_files."}, t.listSiteFiles)
	sdk.AddTool(s, &sdk.Tool{Name: "read_site_files", Annotations: readOnly("Read site files"),
		Description: "Read files of a published site: text as is, small binary files as base64, large ones only " +
			"described. At most 256 KB of content per call; read the rest in further calls. Read the files you are " +
			"going to change, then call update_site_files with the deploy_id returned here."}, t.readSiteFiles)
	sdk.AddTool(s, &sdk.Tool{Name: "update_site_files", Annotations: writes("Change site files", false, false),
		Description: "Change an existing site: add or replace files (whole content), delete files or folders, and " +
			"download files by URL (fetch). Everything else stays as it was. Creates a new version; the previous one " +
			"stays available for rollback. base_deploy_id must be the deploy_id you read the files from: if the " +
			"site changed meanwhile you get a deploy_conflict error; then read the files again and redo the change. " +
			"Give the user the returned link and say in plain words what changed." + fetchAdvice}, t.updateSiteFiles)
	sdk.AddTool(s, &sdk.Tool{Name: "begin_upload", Annotations: writes("Start upload", false, false),
		Description: "Start uploading a site that is larger than 1 MB, in parts. mode replace publishes exactly " +
			"the uploaded files (a new site when site is omitted); mode patch adds them on top of base_deploy_id. " +
			"Then call upload_files (up to 1 MB per call) and commit_upload. The session closes after 30 idle " +
			"minutes; at most 3 can be open."}, t.beginUpload)
	sdk.AddTool(s, &sdk.Tool{Name: "upload_files", Annotations: writes("Upload files", false, true),
		Description: "Send the next part of an upload started with begin_upload: files by value (at most 1 MB per " +
			"call) and/or fetch entries the server downloads. Sending a path again replaces it." + fetchAdvice}, t.uploadFiles)
	sdk.AddTool(s, &sdk.Tool{Name: "commit_upload", Annotations: writes("Publish upload", false, false),
		Description: "Publish an upload as a new version of the site and close the session. index.html is required " +
			"at the root. Give the user the returned link."}, t.commitUpload)
}

func (t tools) filesBackend(ctx context.Context, req *sdk.CallToolRequest) (SiteFilesBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	fb, ok := b.(SiteFilesBackend)
	if !ok {
		return nil, errNoSiteFiles
	}
	return fb, nil
}

func (t tools) listSiteFiles(ctx context.Context, req *sdk.CallToolRequest, in listFilesArgs) (*sdk.CallToolResult, SiteFileList, error) {
	b, err := t.filesBackend(ctx, req)
	if err != nil {
		return nil, SiteFileList{}, err
	}
	out, err := b.ListSiteFiles(ctx, in.Site, in.DeployID, in.Prefix)
	if out.Files == nil {
		out.Files = []SiteFile{}
	}
	return nil, out, err
}

func (t tools) readSiteFiles(ctx context.Context, req *sdk.CallToolRequest, in readFilesArgs) (*sdk.CallToolResult, SiteFileRead, error) {
	b, err := t.filesBackend(ctx, req)
	if err != nil {
		return nil, SiteFileRead{}, err
	}
	out, err := b.ReadSiteFiles(ctx, in.Site, in.DeployID, in.Paths)
	if out.Files == nil {
		out.Files = []SiteFileContent{}
	}
	return nil, out, err
}

func (t tools) updateSiteFiles(ctx context.Context, req *sdk.CallToolRequest, in updateFilesArgs) (*sdk.CallToolResult, FilesResult, error) {
	b, err := t.filesBackend(ctx, req)
	if err != nil {
		return nil, FilesResult{}, err
	}
	out, err := b.UpdateSiteFiles(ctx, FilesChange{
		Site: in.Site, Base: in.BaseDeployID, Files: in.Files, Delete: in.Delete, Fetch: in.Fetch,
	})
	return nil, out, err
}

func (t tools) beginUpload(ctx context.Context, req *sdk.CallToolRequest, in beginUploadArgs) (*sdk.CallToolResult, Upload, error) {
	b, err := t.filesBackend(ctx, req)
	if err != nil {
		return nil, Upload{}, err
	}
	out, err := b.BeginUpload(ctx, UploadStart{Site: in.Site, Title: in.Title, Mode: in.Mode, Base: in.BaseDeployID})
	return nil, out, err
}

func (t tools) uploadFiles(ctx context.Context, req *sdk.CallToolRequest, in uploadFilesArgs) (*sdk.CallToolResult, Upload, error) {
	b, err := t.filesBackend(ctx, req)
	if err != nil {
		return nil, Upload{}, err
	}
	out, err := b.UploadFiles(ctx, in.UploadID, in.Files, in.Fetch)
	return nil, out, err
}

func (t tools) commitUpload(ctx context.Context, req *sdk.CallToolRequest, in commitUploadArgs) (*sdk.CallToolResult, FilesResult, error) {
	b, err := t.filesBackend(ctx, req)
	if err != nil {
		return nil, FilesResult{}, err
	}
	out, err := b.CommitUpload(ctx, in.UploadID, in.Delete)
	return nil, out, err
}
