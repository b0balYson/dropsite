package mcp

import (
	"context"

	"github.com/b0balYson/dropsite/internal/quality"
)

// SiteFile is one file of a release as list_site_files returns it.
type SiteFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size" jsonschema:"size in bytes"`
	SHA256 string `json:"sha256"`
	Type   string `json:"type" jsonschema:"content type guessed from the extension"`
}

// SiteFileList is the result of list_site_files.
type SiteFileList struct {
	SiteID     string     `json:"site_id"`
	URL        string     `json:"url" jsonschema:"primary address of the site"`
	DeployID   string     `json:"deploy_id" jsonschema:"the release that was read: pass it as base_deploy_id to update_site_files"`
	Current    bool       `json:"current" jsonschema:"true when this release is the one being served"`
	Files      []SiteFile `json:"files"`
	TotalFiles int        `json:"total_files"`
	TotalBytes int64      `json:"total_bytes"`
	Truncated  bool       `json:"truncated,omitempty" jsonschema:"true when more files exist than listed; narrow with prefix"`
}

// SiteFileContent is one file read by read_site_files.
type SiteFileContent struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Type     string `json:"type"`
	Encoding string `json:"encoding,omitempty" jsonschema:"utf8 for text, base64 for binary; empty when content is not included"`
	Content  string `json:"content,omitempty"`
	Note     string `json:"note,omitempty" jsonschema:"why the content is not included and what to do instead"`
}

// SiteFileRead is the result of read_site_files.
type SiteFileRead struct {
	SiteID    string            `json:"site_id"`
	DeployID  string            `json:"deploy_id" jsonschema:"the release that was read: pass it as base_deploy_id to update_site_files"`
	Current   bool              `json:"current"`
	Files     []SiteFileContent `json:"files"`
	Missing   []string          `json:"missing,omitempty" jsonschema:"requested paths or patterns that matched no file"`
	Truncated bool              `json:"truncated,omitempty" jsonschema:"some files were left out because of the size limit of one answer; read them in another call"`
}

// FetchFile asks the server to download a file into the site.
type FetchFile struct {
	URL  string `json:"url" jsonschema:"public https:// address of an image, font, stylesheet or script"`
	Path string `json:"path" jsonschema:"where to store it inside the site, e.g. img/hero.jpg or fonts/inter.woff2"`
}

// FilesChange is an update_site_files request.
type FilesChange struct {
	Site   string
	Base   string
	Files  []File
	Delete []string
	Fetch  []FetchFile
	// Draft saves the change as the site's draft.
	Draft bool
}

// FetchedFile reports a downloaded file.
type FetchedFile struct {
	URL          string `json:"url"`
	Path         string `json:"path"`
	Type         string `json:"type"`
	Size         int64  `json:"size"`
	OriginalSize int64  `json:"original_size,omitempty"`
	Resized      bool   `json:"resized,omitempty" jsonschema:"true when a large photo was scaled down to 2000 px wide"`
}

// FilesResult is the outcome of update_site_files or commit_upload.
type FilesResult struct {
	SiteID       string        `json:"site_id"`
	DeployID     string        `json:"deploy_id" jsonschema:"the new release; use it as base_deploy_id for the next change"`
	BaseDeployID string        `json:"base_deploy_id,omitempty"`
	URL          string        `json:"url" jsonschema:"primary address: show this link to the user"`
	URLs         []string      `json:"urls"`
	Written      int           `json:"written" jsonschema:"files written or replaced"`
	Deleted      int           `json:"deleted" jsonschema:"files removed"`
	FileCount    int           `json:"file_count" jsonschema:"files in the new release"`
	Fetched      []FetchedFile `json:"fetched,omitempty"`
	Summary      string        `json:"summary" jsonschema:"short human summary to relay to the user"`
	// Quality is the short quality report of the new release.
	Quality *quality.Summary `json:"quality,omitempty" jsonschema:"quality check of the new version: fix the listed issues with another update; full report: get_quality_report"`
	// Draft and PreviewURL are set for a draft (change staged-publish).
	Draft      bool   `json:"draft,omitempty" jsonschema:"true when the change was saved as a draft: visitors still see the previous version at url"`
	PreviewURL string `json:"preview_url,omitempty" jsonschema:"draft only: the address that shows the draft; give it to the user and ask whether to publish"`
}

// Upload describes an upload session.
type Upload struct {
	UploadID     string        `json:"upload_id"`
	Mode         string        `json:"mode"`
	SiteID       string        `json:"site_id,omitempty"`
	BaseDeployID string        `json:"base_deploy_id,omitempty"`
	Files        int           `json:"files" jsonschema:"files received so far"`
	Bytes        int64         `json:"bytes" jsonschema:"bytes received so far"`
	ExpiresAt    string        `json:"expires_at" jsonschema:"the session closes if idle until then; every upload_files call extends it"`
	MaxBytes     int64         `json:"max_bytes" jsonschema:"size limit of the whole site"`
	MaxFiles     int           `json:"max_files"`
	MaxFileBytes int64         `json:"max_file_bytes"`
	MaxCallBytes int           `json:"max_call_bytes" jsonschema:"at most this many bytes of file content per upload_files call"`
	Fetched      []FetchedFile `json:"fetched,omitempty"`
	// UploadURL and the fields after it come only from begin_upload.
	UploadURL          string `json:"upload_url,omitempty" jsonschema:"upload link of this session: files sent here by HTTP or dropped on its page join the upload without passing through the chat"`
	UploadURLExpiresAt string `json:"upload_url_expires_at,omitempty" jsonschema:"the link stops working if unused until then (one hour, extended by every use)"`
	UploadCommand      string `json:"upload_command,omitempty" jsonschema:"shell command that sends a whole folder to upload_url; replace <folder> with the site folder"`
	NextSteps          string `json:"next_steps,omitempty" jsonschema:"what to do next: follow it"`
}

// UploadStart is a begin_upload request.
type UploadStart struct {
	Site  string
	Title string
	Mode  string
	Base  string
}

// SiteFilesBackend reads and edits the files of published sites. The tools
// fail when the backend does not implement it.
type SiteFilesBackend interface {
	ListSiteFiles(ctx context.Context, ref, deployID, prefix string) (SiteFileList, error)
	ReadSiteFiles(ctx context.Context, ref, deployID string, paths []string) (SiteFileRead, error)
	UpdateSiteFiles(ctx context.Context, c FilesChange) (FilesResult, error)
	BeginUpload(ctx context.Context, u UploadStart) (Upload, error)
	UploadFiles(ctx context.Context, uploadID string, files []File, fetch []FetchFile) (Upload, error)
	CommitUpload(ctx context.Context, uploadID string, deletes []string) (FilesResult, error)
}
