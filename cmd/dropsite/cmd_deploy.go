package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
)

type deployOpts struct {
	dir     string
	site    string
	title   string
	expires string
	asJSON  bool
	expiry  *client.Expiry // parsed --expires; nil leaves the expiry alone
	// sensitive decides about secret-looking files; sensitiveHints explain
	// how to override a refusal in the caller's terms.
	sensitive      sensitiveMode
	sensitiveHints []string
}

// deployOutput is the --json result of deploy and rollback.
type deployOutput struct {
	SiteID   string   `json:"site_id"`
	DeployID string   `json:"deploy_id"`
	URL      string   `json:"url"`
	URLs     []string `json:"urls"`
}

func cmdDeploy(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("deploy", "<dir> [--site <id|slug>] [--title <text>] [--expires <when>] [--skip-sensitive|--include-sensitive] [--json]",
		"Pack <dir> (it must contain index.html), publish it and print the site URL.\n"+
			"Without --site the site is taken from "+brand.ProjectFile+" in <dir>; if there is\n"+
			"none, a new site is created and remembered there. A folder with secret-looking\n"+
			"files (.env*, .npmrc, .netrc, .git-credentials, .ssh/, .aws/, *.pem, *.key,\n"+
			"id_rsa*) is refused unless --skip-sensitive or --include-sensitive is given.")
	var o deployOpts
	fs.StringVar(&o.site, "site", "", "publish to this site (ID or slug)")
	fs.StringVar(&o.title, "title", "", "title of a newly created site (default: the directory name)")
	fs.StringVar(&o.expires, "expires", "", "take the site down after: 7d, 36h, 2026-12-31 (end of day, UTC), an RFC 3339 time, or never")
	fs.BoolVar(&o.asJSON, "json", false, "print one JSON object {site_id, deploy_id, url, urls}")
	skipSensitive := fs.Bool("skip-sensitive", false, "publish without secret-looking files (.env*, keys, credentials) instead of stopping")
	includeSensitive := fs.Bool("include-sensitive", false, "publish secret-looking files too (only if they are really meant to be public)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 1, 1); err != nil {
		return err
	}
	switch {
	case *skipSensitive && *includeSensitive:
		return usagef("--skip-sensitive and --include-sensitive cannot be used together")
	case *skipSensitive:
		o.sensitive = sensitiveSkip
	case *includeSensitive:
		o.sensitive = sensitiveInclude
	}
	o.sensitiveHints = cliSensitiveHints
	o.dir = pos[0]
	if o.expires != "" {
		if o.expiry, err = parseExpires(o.expires, time.Now()); err != nil {
			return err
		}
	}
	return a.deploy(ctx, o)
}

func (a *app) deploy(ctx context.Context, o deployOpts) error {
	c, err := a.connect()
	if err != nil {
		return err
	}
	res, err := a.publishDir(ctx, c, o)
	if err != nil {
		return err
	}
	if err := a.printDeploy(res, o.asJSON); err != nil {
		return err
	}
	return a.setExpiry(ctx, c, res.Site.ID, o.expiry)
}

// setExpiry applies --expires after a successful publication.
func (a *app) setExpiry(ctx context.Context, c *client.Client, siteID string, e *client.Expiry) error {
	if e == nil {
		return nil
	}
	s, err := c.UpdateSite(ctx, siteID, client.UpdateSiteRequest{ExpiresAt: e})
	if err != nil {
		return withHint(fmt.Errorf("the site is published, but its expiry was not set: %w", err),
			"Run the command again with a valid --expires, or set the date in the web interface.")
	}
	if s.ExpiresAt == nil {
		fmt.Fprintln(a.stderr, "The site has no expiry date.")
		return nil
	}
	fmt.Fprintf(a.stderr, "The site expires at %s.\n", s.ExpiresAt.UTC().Format(time.RFC3339))
	return nil
}

// publishDir packs and publishes o.dir; progress goes to stderr only, so the
// MCP server can reuse it without touching stdout.
func (a *app) publishDir(ctx context.Context, c *client.Client, o deployOpts) (*client.DeployResult, error) {
	if err := checkDeployDir(o.dir); err != nil {
		return nil, err
	}
	skip, err := a.checkSensitive(ctx, o)
	if err != nil {
		return nil, err
	}
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	p := startPack(ctx, o.dir, packLimits(me.Limits), skip)
	if err := p.ready(); err != nil {
		return nil, err
	}
	siteID, err := a.resolveSite(ctx, c, o, me.User.ID)
	if err != nil {
		return nil, errors.Join(err, p.stop())
	}
	return a.upload(ctx, c, siteID, o.dir, p)
}

func packLimits(l client.Limits) archive.Limits {
	return archive.Limits{MaxTotalBytes: l.MaxSiteBytes, MaxFileBytes: l.MaxFileBytes, MaxFiles: l.MaxFiles}
}

func (a *app) upload(ctx context.Context, c *client.Client, siteID, dir string, p *packer) (*client.DeployResult, error) {
	fmt.Fprintf(a.stderr, "Uploading %s to site %s...\n", dir, siteID)
	var sent atomic.Int64
	res, err := c.Deploy(ctx, siteID, p, func(n int64) { sent.Store(n) })
	packErr := p.stop()
	if err != nil {
		return nil, uploadError(siteID, err, packErr)
	}
	if packErr != nil {
		return nil, packErr
	}
	fmt.Fprintf(a.stderr, "Published %d files (%s, %s compressed) as deploy %s\n",
		p.res.Files, formatBytes(p.res.Bytes), formatBytes(sent.Load()), res.DeployID)
	if res.Quality != nil {
		fmt.Fprintln(a.stderr, res.Quality.Summary)
	}
	return res, nil
}

// uploadError prefers the server's verdict, then a local packing failure.
func uploadError(siteID string, err, packErr error) error {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return siteError(siteID, err)
	}
	if packErr != nil {
		return packErr
	}
	return err
}

func (a *app) printDeploy(res *client.DeployResult, asJSON bool) error {
	if asJSON {
		return writeJSON(a.stdout, deployOutput{SiteID: res.Site.ID, DeployID: res.DeployID, URL: res.URL, URLs: res.URLs})
	}
	fmt.Fprintln(a.stdout, res.URL)
	return nil
}

// resolveSite picks the target: --site, the project file, or a new site.
// userID is the logged-in user; a project file may only name their own site.
func (a *app) resolveSite(ctx context.Context, c *client.Client, o deployOpts, userID string) (string, error) {
	if o.site != "" {
		return lookupSite(ctx, c, o.site)
	}
	proj, ok, err := readProject(o.dir)
	if err != nil {
		return "", err
	}
	if ok {
		return linkedSite(ctx, c, o.dir, proj, userID)
	}
	return a.createLinkedSite(ctx, c, o)
}

func lookupSite(ctx context.Context, c *client.Client, ref string) (string, error) {
	s, err := c.GetSite(ctx, ref)
	if err != nil {
		return "", siteError(ref, err)
	}
	return s.ID, nil
}

// linkedSite resolves the site remembered in the project file. A site owned by
// someone else is refused: an administrator sees every site, so a project
// file from a cloned repository could otherwise silently overwrite it.
func linkedSite(ctx context.Context, c *client.Client, dir string, p project, userID string) (string, error) {
	server, err := normalizeServer(p.Server)
	if err != nil || server != c.Server() {
		return "", withHint(
			fmt.Errorf("%s links this directory to site %s on %s, but you are logged in to %s",
				projectPath(dir), p.SiteID, p.Server, c.Server()),
			"Log in to that server, pass --site to publish to a site on the current server,",
			fmt.Sprintf("or delete %s to create a new site here.", brand.ProjectFile))
	}
	s, err := c.GetSite(ctx, p.SiteID)
	if err != nil {
		return "", siteGoneError(p.SiteID, err)
	}
	if s.OwnerID != userID {
		return "", withHint(
			fmt.Errorf("%s links this directory to site %s, which belongs to another user", projectPath(dir), s.ID),
			"Nothing was published. If you really mean to publish to that site, pass it explicitly:",
			fmt.Sprintf("  %s deploy %s --site %s", brand.Name, dir, s.ID),
			fmt.Sprintf("Otherwise delete %s to publish as a new site of your own.", brand.ProjectFile))
	}
	return s.ID, nil
}

func (a *app) createLinkedSite(ctx context.Context, c *client.Client, o deployOpts) (string, error) {
	title := o.title
	if title == "" {
		title = dirTitle(o.dir)
	}
	s, err := c.CreateSite(ctx, client.CreateSiteRequest{Title: title})
	if err != nil {
		return "", fmt.Errorf("create site: %w", err)
	}
	if err := writeProject(o.dir, project{Server: c.Server(), SiteID: s.ID}); err != nil {
		return "", err
	}
	fmt.Fprintf(a.stderr, "Created site %s (%s) and saved it to %s\n", s.ID, s.URL, projectPath(o.dir))
	return s.ID, nil
}

func dirTitle(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Base(dir)
	}
	return filepath.Base(abs)
}

func isNotFound(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && ae.IsNotFound()
}

// siteError explains a 404 for a site named on the command line.
func siteError(ref string, err error) error {
	if !isNotFound(err) {
		return err
	}
	return withHint(fmt.Errorf("site %s: %w", ref, err),
		fmt.Sprintf("Run '%s sites' to see the IDs and slugs of your sites.", brand.Name))
}

// siteGoneError explains a 404 for the site remembered in the project file.
func siteGoneError(ref string, err error) error {
	if !isNotFound(err) {
		return err
	}
	return withHint(fmt.Errorf("site %s from %s: %w", ref, brand.ProjectFile, err),
		"The site no longer exists on this server (deleted, or owned by another account). Nothing was created.",
		fmt.Sprintf("Delete %s to publish as a new site, or pass --site <id|slug>.", brand.ProjectFile))
}
