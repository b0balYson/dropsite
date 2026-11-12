package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type siteArgs struct {
	Site string `json:"site" jsonschema:"site ID or slug, as returned by list_sites"`
}

type createArgs struct {
	Title string `json:"title" jsonschema:"site name shown in the dropsite interface"`
	Slug  string `json:"slug,omitempty" jsonschema:"optional memorable address label (a-z, 0-9, hyphens), e.g. promo-may"`
}

type slugArgs struct {
	Site string `json:"site" jsonschema:"site ID or slug"`
	Slug string `json:"slug" jsonschema:"new address label (a-z, 0-9, hyphens); an empty string removes the slug"`
}

type rollbackArgs struct {
	Site     string `json:"site" jsonschema:"site ID or slug"`
	DeployID string `json:"deploy_id,omitempty" jsonschema:"release to switch to; empty means the previous release"`
}

type deleteArgs struct {
	Site    string `json:"site" jsonschema:"site ID or slug"`
	Confirm string `json:"confirm" jsonschema:"the site's slug or ID, typed again after the user explicitly confirmed the deletion"`
}

type filesArgs struct {
	Site  string `json:"site,omitempty" jsonschema:"site ID or slug to update; omit to create a new site"`
	Title string `json:"title,omitempty" jsonschema:"name of the new site when site is omitted"`
	Files []File `json:"files" jsonschema:"every file of the site; index.html is required at the root; at most 1 MB in total"`
}

type folderArgs struct {
	Path  string `json:"path" jsonschema:"absolute path of a folder on this computer that contains index.html"`
	Site  string `json:"site,omitempty" jsonschema:"site ID or slug; omit to use the site remembered in the folder or create a new one"`
	Title string `json:"title,omitempty" jsonschema:"name of a newly created site (default: the folder name)"`
}

type sitesOut struct {
	Sites []Site `json:"sites"`
}

type deploysOut struct {
	Deploys []Deploy `json:"deploys" jsonschema:"releases, newest first"`
}

type deleteOut struct {
	Deleted string `json:"deleted" jsonschema:"ID of the deleted site"`
}

type tools struct {
	resolve Resolver
}

func boolPtr(b bool) *bool { return &b }

func readOnly(title string) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
}

func writes(title string, destructive, idempotent bool) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{Title: title, DestructiveHint: boolPtr(destructive), IdempotentHint: idempotent,
		OpenWorldHint: boolPtr(false)}
}

func (t tools) register(s *sdk.Server, o Options) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_sites", Annotations: readOnly("List sites"),
		Description: "List the user's dropsite sites with their addresses."}, t.listSites)
	sdk.AddTool(s, &sdk.Tool{Name: "create_site", Annotations: writes("Create site", false, false),
		Description: "Create an empty site and return its address. Publish content into it afterwards."}, t.createSite)
	sdk.AddTool(s, &sdk.Tool{Name: "get_site", Annotations: readOnly("Get site"),
		Description: "Get one site: addresses and the release being served."}, t.getSite)
	sdk.AddTool(s, &sdk.Tool{Name: "set_slug", Annotations: writes("Set address", false, true),
		Description: "Set, change or remove (empty slug) the memorable address of a site, e.g. promo-may.<domain>."}, t.setSlug)
	sdk.AddTool(s, &sdk.Tool{Name: "list_deploys", Annotations: readOnly("List releases"),
		Description: "List the releases of a site, newest first, marking the current one."}, t.listDeploys)
	sdk.AddTool(s, &sdk.Tool{Name: "rollback", Annotations: writes("Roll back", false, false),
		Description: "Switch a site to an earlier release (the previous one when deploy_id is empty)."}, t.rollback)
	sdk.AddTool(s, &sdk.Tool{Name: "delete_site", Annotations: writes("Delete site", true, true),
		Description: "Delete a site and all its releases; its addresses stop working. Only after the user " +
			"explicitly confirmed: pass the site's slug or ID again in confirm."}, t.deleteSite)
	if o.Files {
		sdk.AddTool(s, &sdk.Tool{Name: "deploy_files", Annotations: writes("Publish files", false, false),
			Description: "Publish a small static site from files passed by value (at most 1 MB in total) and " +
				"return its link. Creates a new site when site is omitted. index.html is required at the root. " +
				"For bigger sites tell the user to publish with the dropsite CLI or Claude Desktop."}, t.deployFiles)
	}
	if o.Folder {
		sdk.AddTool(s, &sdk.Tool{Name: "deploy_folder", Annotations: writes("Publish folder", false, false),
			Description: "Publish a folder from this computer (it must contain index.html) and return its link. " +
				"The first publication creates a site and remembers it in .dropsite.json inside the folder; " +
				"later publications of the same folder update that site."}, t.deployFolder)
	}
}

func (t tools) listSites(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, sitesOut, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, sitesOut{}, err
	}
	sites, err := b.ListSites(ctx)
	if sites == nil {
		sites = []Site{}
	}
	return nil, sitesOut{Sites: sites}, err
}

func (t tools) createSite(ctx context.Context, req *sdk.CallToolRequest, in createArgs) (*sdk.CallToolResult, Site, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, Site{}, err
	}
	s, err := b.CreateSite(ctx, in.Title, in.Slug)
	return nil, s, err
}

func (t tools) getSite(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, Site, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, Site{}, err
	}
	s, err := b.GetSite(ctx, in.Site)
	return nil, s, err
}

func (t tools) setSlug(ctx context.Context, req *sdk.CallToolRequest, in slugArgs) (*sdk.CallToolResult, Site, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, Site{}, err
	}
	s, err := b.SetSlug(ctx, in.Site, in.Slug)
	return nil, s, err
}

func (t tools) listDeploys(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, deploysOut, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, deploysOut{}, err
	}
	ds, err := b.ListDeploys(ctx, in.Site)
	if ds == nil {
		ds = []Deploy{}
	}
	return nil, deploysOut{Deploys: ds}, err
}

func (t tools) rollback(ctx context.Context, req *sdk.CallToolRequest, in rollbackArgs) (*sdk.CallToolResult, DeployResult, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, DeployResult{}, err
	}
	r, err := b.Rollback(ctx, in.Site, in.DeployID)
	return nil, r, err
}

func (t tools) deleteSite(ctx context.Context, req *sdk.CallToolRequest, in deleteArgs) (*sdk.CallToolResult, deleteOut, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, deleteOut{}, err
	}
	s, err := b.GetSite(ctx, in.Site)
	if err != nil {
		return nil, deleteOut{}, err
	}
	if !sameRef(in.Confirm, s) {
		return nil, deleteOut{}, ErrConfirm
	}
	if err := b.DeleteSite(ctx, s.ID); err != nil {
		return nil, deleteOut{}, err
	}
	return nil, deleteOut{Deleted: s.ID}, nil
}

var errNoPublisher = errors.New("this server cannot publish")

func (t tools) deployFiles(ctx context.Context, req *sdk.CallToolRequest, in filesArgs) (*sdk.CallToolResult, DeployResult, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, DeployResult{}, err
	}
	d, ok := b.(FilesDeployer)
	if !ok {
		return nil, DeployResult{}, errNoPublisher
	}
	r, err := d.DeployFiles(ctx, in.Site, in.Title, in.Files)
	return nil, r, err
}

func (t tools) deployFolder(ctx context.Context, req *sdk.CallToolRequest, in folderArgs) (*sdk.CallToolResult, DeployResult, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, DeployResult{}, err
	}
	d, ok := b.(FolderDeployer)
	if !ok {
		return nil, DeployResult{}, errNoPublisher
	}
	r, err := d.DeployFolder(ctx, in.Path, in.Site, in.Title)
	return nil, r, err
}
