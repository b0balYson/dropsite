// Package mcp defines the MCP tools of dropsite. The same tools are served by
// the remote endpoint on the API host (internal/mcp/remote, backed by
// internal/service) and by the local stdio server of the CLI (backed by the
// HTTP API). This package must not import the server: the CLI links it.
package mcp

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/quality"
)

// Site is a site as tools return it.
type Site struct {
	ID            string   `json:"id" jsonschema:"site ID"`
	Title         string   `json:"title"`
	URL           string   `json:"url" jsonschema:"primary address: show this link to the user"`
	URLs          []string `json:"urls" jsonschema:"every address of the site, primary first"`
	Slug          string   `json:"slug,omitempty" jsonschema:"memorable address label, if set"`
	CurrentDeploy *Deploy  `json:"current_deploy,omitempty" jsonschema:"the release being served, if any"`
	LastDeployAt  string   `json:"last_deploy_at,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
	// WorkspaceID and Workspace name the shared workspace of the site.
	WorkspaceID string `json:"workspace_id,omitempty" jsonschema:"shared workspace of the site; absent for the user's personal sites"`
	Workspace   string `json:"workspace,omitempty" jsonschema:"name of the shared workspace: every member can change the site"`
	SiteExtras
	// Draft is the site's draft release (change staged-publish).
	Draft *Draft `json:"draft,omitempty" jsonschema:"a new version visitors do not see yet: open preview_url to check it, publish_draft makes it live"`
}

// SiteDetail is a site as get_site returns it: the site and what is
// attached to it. Only get_site carries attachments, so the other tools keep
// the shorter Site schema.
type SiteDetail struct {
	Site
	Attached *SiteAttached `json:"attached,omitempty" jsonschema:"what is attached to the site: forms, delivery channels, counters, data collections, API key proxies"`
}

// Deploy is a release.
type Deploy struct {
	ID        string `json:"id"`
	Source    string `json:"source" jsonschema:"ui, api, cli or mcp"`
	SizeBytes int64  `json:"size_bytes"`
	FileCount int    `json:"file_count"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Current   bool   `json:"current" jsonschema:"true for the release being served"`
	// PreviewURL is the release's own address, when the server has previews.
	PreviewURL string `json:"preview_url,omitempty" jsonschema:"address that always serves this exact release, if previews are enabled"`
	Draft      bool   `json:"draft,omitempty" jsonschema:"true for the site's draft: not served to visitors yet"`
}

// DeployResult is the outcome of a publication or rollback.
type DeployResult struct {
	SiteID   string   `json:"site_id"`
	DeployID string   `json:"deploy_id"`
	URL      string   `json:"url" jsonschema:"primary address: show this link to the user"`
	URLs     []string `json:"urls"`
	// Quality is the short quality report of the release being served.
	Quality *quality.Summary `json:"quality,omitempty" jsonschema:"quality check of this version: fix the listed issues with update_site_files; full report: get_quality_report"`
	// Draft and PreviewURL are set for a draft (change staged-publish).
	Draft      bool   `json:"draft,omitempty" jsonschema:"true when this version was saved as a draft: visitors still see the previous one at url"`
	PreviewURL string `json:"preview_url,omitempty" jsonschema:"draft only: the address that shows the draft; give it to the user and ask whether to publish"`
}

// File is one file passed by value to deploy_files.
type File struct {
	Path     string `json:"path" jsonschema:"relative path inside the site, e.g. index.html or css/site.css"`
	Content  string `json:"content" jsonschema:"file content: text as is, or base64 when encoding is base64"`
	Encoding string `json:"encoding,omitempty" jsonschema:"utf8 (default) for text, base64 for binary files such as images"`
}

// Backend performs the operations for one authenticated user.
type Backend interface {
	ListSites(ctx context.Context) ([]Site, error)
	CreateSite(ctx context.Context, title, slug string) (Site, error)
	GetSite(ctx context.Context, ref string) (Site, error)
	SetSlug(ctx context.Context, ref, slug string) (Site, error)
	ListDeploys(ctx context.Context, ref string) ([]Deploy, error)
	Rollback(ctx context.Context, ref, deployID string) (DeployResult, error)
	DeleteSite(ctx context.Context, ref string) error
}

// FilesDeployer publishes files passed by value (remote server).
type FilesDeployer interface {
	DeployFiles(ctx context.Context, ref, title string, files []File) (DeployResult, error)
}

// FolderDeployer publishes a folder from the local disk (CLI server).
type FolderDeployer interface {
	DeployFolder(ctx context.Context, f Folder) (DeployResult, error)
}

// Folder is a deploy_folder request.
type Folder struct {
	Path  string
	Site  string
	Title string
	// SkipSensitive publishes without secret-looking files instead of
	// refusing the folder.
	SkipSensitive bool
	// Draft saves the folder as the site's draft.
	Draft bool
}

// Resolver returns the backend for a tool call (per authenticated user), or
// an error the model can act on.
type Resolver func(ctx context.Context, req *sdk.CallToolRequest) (Backend, error)

// Options selects the tool groups. Both servers offer the same tools except
// the publication tool: deploy_files (remote) or deploy_folder (stdio).
type Options struct {
	// Files adds deploy_files; the backend must implement FilesDeployer.
	Files bool
	// Folder adds deploy_folder; the backend must implement FolderDeployer.
	Folder bool
	// SiteFiles adds the tools that read and edit site files and upload
	// large sites in parts; the backend must implement SiteFilesBackend.
	SiteFiles bool
	// Forms adds the forms tools; the backend must implement FormsBackend.
	Forms bool
	// Cabinet adds the account and site settings tools; the backend must
	// implement Cabinet.
	Cabinet bool
	// Collections adds the site data tools; the backend must implement
	// CollectionsBackend.
	Collections bool
	// Workflow adds site copies, templates, scheduled publishes and
	// screenshots; the backend must implement WorkflowBackend.
	Workflow bool
	// Drafts adds publish_draft and discard_draft; the backend must
	// implement DraftBackend.
	Drafts bool
	// Instructions are sent to the client on initialize.
	Instructions string
}

// NewServer builds an MCP server with the dropsite tools.
func NewServer(version string, resolve Resolver, o Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: brand.Name, Title: brand.DisplayName, Version: version},
		&sdk.ServerOptions{Instructions: o.Instructions})
	t := tools{resolve: resolve}
	t.register(s, o)
	return s
}

// ErrConfirm is returned by delete_site when confirm does not name the site.
var ErrConfirm = errors.New("not deleted: ask the user to confirm, then call delete_site again with confirm set " +
	"to the site's slug or ID exactly as shown by get_site")

// sameRef reports whether confirm names the site.
func sameRef(confirm string, s Site) bool {
	c := strings.ToLower(strings.TrimSpace(confirm))
	return c != "" && (c == strings.ToLower(s.ID) || (s.Slug != "" && c == strings.ToLower(s.Slug)))
}
