package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// SiteFile is one file of a release.
type SiteFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Type   string `json:"type"`
}

// SiteFileList is the listing of a release.
type SiteFileList struct {
	SiteID     string     `json:"site_id"`
	URL        string     `json:"url"`
	DeployID   string     `json:"deploy_id"`
	Current    bool       `json:"current"`
	Files      []SiteFile `json:"files"`
	TotalFiles int        `json:"total_files"`
	TotalBytes int64      `json:"total_bytes"`
	Truncated  bool       `json:"truncated"`
}

// SiteFileContent is a read file; Encoding is utf8 or base64, empty when
// the content is not included (Note says why).
type SiteFileContent struct {
	SiteFile
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Note     string `json:"note"`
}

// SiteFileRead is the response of reading files.
type SiteFileRead struct {
	SiteID    string            `json:"site_id"`
	DeployID  string            `json:"deploy_id"`
	Current   bool              `json:"current"`
	Files     []SiteFileContent `json:"files"`
	Missing   []string          `json:"missing"`
	Truncated bool              `json:"truncated"`
}

// InlineFile is a file passed by value.
type InlineFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty"`
}

// FetchSpec asks the server to download URL into the site at Path.
type FetchSpec struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

// FetchedFile reports a downloaded asset.
type FetchedFile struct {
	URL           string `json:"url"`
	Path          string `json:"path"`
	Type          string `json:"type"`
	Bytes         int64  `json:"bytes"`
	OriginalBytes int64  `json:"original_bytes"`
	Resized       bool   `json:"resized"`
}

// FilesChange edits a site on top of BaseDeployID.
type FilesChange struct {
	BaseDeployID string       `json:"base_deploy_id"`
	Files        []InlineFile `json:"files,omitempty"`
	Delete       []string     `json:"delete,omitempty"`
	Fetch        []FetchSpec  `json:"fetch,omitempty"`
	// Draft builds the release as the site's draft.
	Draft bool `json:"draft,omitempty"`
}

// FilesResult is a release made by a files edit or an upload commit.
type FilesResult struct {
	DeployResult
	BaseDeployID string        `json:"base_deploy_id"`
	Written      int           `json:"written"`
	Deleted      int           `json:"deleted"`
	Fetched      []FetchedFile `json:"fetched"`
}

// UploadStart opens an upload session.
type UploadStart struct {
	Site         string `json:"site,omitempty"`
	Title        string `json:"title,omitempty"`
	Mode         string `json:"mode,omitempty"`
	BaseDeployID string `json:"base_deploy_id,omitempty"`
}

// Upload is an open upload session.
type Upload struct {
	ID           string        `json:"id"`
	Mode         string        `json:"mode"`
	SiteID       string        `json:"site_id"`
	BaseDeployID string        `json:"base_deploy_id"`
	Files        int           `json:"files"`
	Bytes        int64         `json:"bytes"`
	ExpiresAt    time.Time     `json:"expires_at"`
	MaxBytes     int64         `json:"max_bytes"`
	MaxFiles     int           `json:"max_files"`
	MaxFileBytes int64         `json:"max_file_bytes"`
	MaxCallBytes int           `json:"max_call_bytes"`
	Fetched      []FetchedFile `json:"fetched"`
	// UploadURL is the upload link; only BeginUpload returns it.
	UploadURL          string     `json:"upload_url,omitempty"`
	UploadURLExpiresAt *time.Time `json:"upload_url_expires_at,omitempty"`
}

func filesPath(ref string) string { return sitePath(ref) + "/files" }

func uploadPath(id string) string { return "/uploads/" + url.PathEscape(id) }

// ListSiteFiles lists a release (deployID empty: the current one).
func (c *Client) ListSiteFiles(ctx context.Context, ref, deployID, prefix string) (*SiteFileList, error) {
	q := url.Values{}
	if deployID != "" {
		q.Set("deploy_id", deployID)
	}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	var out SiteFileList
	if err := c.doJSON(ctx, http.MethodGet, filesPath(ref), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type readFilesRequest struct {
	DeployID string   `json:"deploy_id,omitempty"`
	Paths    []string `json:"paths"`
}

// ReadSiteFiles reads files or patterns of a release.
func (c *Client) ReadSiteFiles(ctx context.Context, ref, deployID string, paths []string) (*SiteFileRead, error) {
	if paths == nil {
		paths = []string{}
	}
	var out SiteFileRead
	if err := c.doJSON(ctx, http.MethodPost, filesPath(ref)+"/read", nil, readFilesRequest{DeployID: deployID, Paths: paths}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSiteFiles edits a site and publishes a new release.
func (c *Client) UpdateSiteFiles(ctx context.Context, ref string, ch FilesChange) (*FilesResult, error) {
	var out FilesResult
	if err := c.doJSON(ctx, http.MethodPatch, filesPath(ref), nil, ch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BeginUpload opens an upload session.
func (c *Client) BeginUpload(ctx context.Context, u UploadStart) (*Upload, error) {
	var out Upload
	if err := c.doJSON(ctx, http.MethodPost, "/uploads", nil, u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type uploadFilesRequest struct {
	Files []InlineFile `json:"files,omitempty"`
	Fetch []FetchSpec  `json:"fetch,omitempty"`
}

// UploadFiles adds files or downloads to a session.
func (c *Client) UploadFiles(ctx context.Context, id string, files []InlineFile, fetch []FetchSpec) (*Upload, error) {
	var out Upload
	if err := c.doJSON(ctx, http.MethodPost, uploadPath(id)+"/files", nil, uploadFilesRequest{Files: files, Fetch: fetch}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type commitUploadRequest struct {
	Delete []string `json:"delete,omitempty"`
	Draft  bool     `json:"draft,omitempty"`
}

// CommitUpload publishes a session and closes it.
func (c *Client) CommitUpload(ctx context.Context, id string, deletes []string) (*FilesResult, error) {
	return c.CommitUploadWith(ctx, id, deletes, false)
}
