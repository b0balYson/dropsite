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
	Files []File `json:"files" jsonschema:"every file of the site; index.html is required at the root; at most 16 MB in total"`
	Draft bool   `json:"draft,omitempty" jsonschema:"save as the site's draft instead of going live (default for a site that is already live, see get_guide topic drafts)"`
}

type folderArgs struct {
	Path          string `json:"path" jsonschema:"absolute path of the site's build output folder (with index.html), not the project folder"`
	Site          string `json:"site,omitempty" jsonschema:"site ID or slug; omit to use the site remembered in the folder or create a new one"`
	Title         string `json:"title,omitempty" jsonschema:"name of a newly created site (default: the folder name)"`
	SkipSensitive bool   `json:"skip_sensitive_files,omitempty" jsonschema:"publish without secret-looking files (.env, keys, credentials) instead of refusing the folder"`
	Draft         bool   `json:"draft,omitempty" jsonschema:"save as the site's draft instead of going live (default for a site that is already live, see get_guide topic drafts)"`
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
	registerGuide(s)
	sdk.AddTool(s, &sdk.Tool{Name: "list_sites", Annotations: readOnly("List sites"),
		Description: "List the user's dropsite sites with names, links and current versions. Call it first " +
			"whenever the user talks about \"my site\" or gives a link, to find which site they mean; if several " +
			"fit, show their names and links and ask. Rules for working with sites: get_guide."}, t.listSites)
	sdk.AddTool(s, &sdk.Tool{Name: "create_site", Annotations: writes("Create site", false, false),
		Description: "Create an empty site and return its link, optionally with a memorable address (slug). " +
			"Usually not needed: deploy_files without site creates and publishes a new site in one step. Use it " +
			"when the user wants an address reserved before the content is ready."}, t.createSite)
	sdk.AddTool(s, &sdk.Tool{Name: "get_site", Annotations: readOnly("Get site"),
		Description: "Get one site: its name, every link (primary first), the version being served, password, " +
			"search engine and expiry settings, own domains, the draft waiting to be published, and what is " +
			"attached (forms with requests, delivery channels, analytics counters). Call it when a chat turns to an existing site, and build on what is " +
			"already set up instead of offering it again. Show the user the primary link."}, t.getSite)
	sdk.AddTool(s, &sdk.Tool{Name: "set_slug", Annotations: writes("Set address", false, true),
		Description: "Set, change or remove (empty slug) the memorable address of a site, e.g. coffee-river." +
			"<base domain>. The random address keeps working. If the label is taken, suggest variants. Tell the " +
			"user the new link; it may need a minute to open the first time."}, t.setSlug)
	sdk.AddTool(s, &sdk.Tool{Name: "list_deploys", Annotations: readOnly("List versions"),
		Description: "List the versions (releases) of a site, newest first, marking the one being served, with " +
			"date, origin and, when enabled, a preview link of each version; the draft, if any, is marked. Use it when the user asks about " +
			"earlier versions; describe them by date and origin, not by ID."}, t.listDeploys)
	sdk.AddTool(s, &sdk.Tool{Name: "rollback", Annotations: writes("Restore version", false, false),
		Description: "Serve an earlier version of a site again: the previous one when deploy_id is empty " +
			"(\"bring back the previous version\"), or the chosen one. Instant, deletes nothing, and can be undone " +
			"the same way. Reply with the link."}, t.rollback)
	sdk.AddTool(s, &sdk.Tool{Name: "delete_site", Annotations: writes("Delete site", true, true),
		Description: "Delete a site with all its versions; its links stop working and this cannot be undone. " +
			"Only after the user explicitly confirmed the deletion of this site: tell them which site (name and " +
			"link) will be deleted, wait for yes, then pass the site's slug or ID again in confirm."}, t.deleteSite)
	if o.Files {
		sdk.AddTool(s, &sdk.Tool{Name: "deploy_files", Annotations: writes("Publish files", false, false),
			Description: "Publish a new static site, or replace a whole site, from files passed by value (at most " +
				"16 MB in total) and return its link to give to the user. Creates a new site when site is omitted. " +
				"index.html is required at the root. Every file not passed is gone from the new version: to change " +
				"an existing site use list_site_files, read_site_files and update_site_files instead. For sites " +
				"larger than 16 MB use begin_upload, upload_files and commit_upload. Ship every image, font and script with the " +
				"site (no CDNs or hotlinks; see get_guide topic assets). Reply \"Your site is live: <link>\"." + draftAdvice}, t.deployFiles)
	}
	t.registerFiles(s, o)
	if o.Folder {
		sdk.AddTool(s, &sdk.Tool{Name: "deploy_folder", Annotations: writes("Publish folder", false, false),
			Description: "Publish a folder from this computer (it must contain index.html) and return its link. " +
				"Everything in the folder becomes public: pass only the site's build output folder (dist, build, " +
				"public), never a project, home or system folder. The home folder, folders directly inside it and " +
				"file system roots are refused, and so is a folder with secret-looking files (.env, keys, " +
				"credentials) unless skip_sensitive_files is true, which leaves them out. " +
				"The first publication creates a site and remembers it in .dropsite.json inside the folder; " +
				"later publications of the same folder update that site." + draftAdvice}, t.deployFolder)
	}
	t.registerForms(s, o)
	t.registerCabinet(s, o)
	t.registerCollections(s, o)
	t.registerSheets(s, o)
	t.registerPublish(s, o)
	t.registerQuality(s, o)
	t.registerWorkflow(s, o)
	t.registerReferences(s, o)
	t.registerDrafts(s, o)
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
	if err != nil {
		return nil, Site{}, err
	}
	if at, ok := b.(SiteAttacher); ok {
		a, err := at.SiteAttachments(ctx, s.ID)
		if err != nil {
			return nil, Site{}, err
		}
		s.Attached = &a
	}
	return nil, s, nil
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
	if in.Draft {
		dd, ok := b.(DraftFilesDeployer)
		if !ok {
			return nil, DeployResult{}, errNoDrafts
		}
		r, err := dd.DeployFilesDraft(ctx, in.Site, in.Title, in.Files)
		return nil, r, err
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
	r, err := d.DeployFolder(ctx, Folder(in))
	return nil, r, err
}
