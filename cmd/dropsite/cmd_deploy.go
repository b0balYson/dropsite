package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
)

type deployOpts struct {
	dir    string
	site   string
	title  string
	asJSON bool
}

// deployOutput is the --json result of deploy and rollback.
type deployOutput struct {
	SiteID   string   `json:"site_id"`
	DeployID string   `json:"deploy_id"`
	URL      string   `json:"url"`
	URLs     []string `json:"urls"`
}

func cmdDeploy(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("deploy", "<dir> [--site <id|slug>] [--title <text>] [--json]",
		"Pack <dir> (it must contain index.html), publish it and print the site URL.\n"+
			"Without --site the site is taken from "+brand.ProjectFile+" in <dir>; if there is\n"+
			"none, a new site is created and remembered there.")
	var o deployOpts
	fs.StringVar(&o.site, "site", "", "publish to this site (ID or slug)")
	fs.StringVar(&o.title, "title", "", "title of a newly created site (default: the directory name)")
	fs.BoolVar(&o.asJSON, "json", false, "print one JSON object {site_id, deploy_id, url, urls}")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 1, 1); err != nil {
		return err
	}
	o.dir = pos[0]
	return a.deploy(ctx, o)
}

func (a *app) deploy(ctx context.Context, o deployOpts) error {
	if err := checkDeployDir(o.dir); err != nil {
		return err
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	p := startPack(ctx, o.dir, packLimits(me.Limits))
	if err := p.ready(); err != nil {
		return err
	}
	siteID, err := a.resolveSite(ctx, c, o)
	if err != nil {
		return errors.Join(err, p.stop())
	}
	res, err := a.upload(ctx, c, siteID, o.dir, p)
	if err != nil {
		return err
	}
	return a.printDeploy(res, o.asJSON)
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
func (a *app) resolveSite(ctx context.Context, c *client.Client, o deployOpts) (string, error) {
	if o.site != "" {
		return lookupSite(ctx, c, o.site)
	}
	proj, ok, err := readProject(o.dir)
	if err != nil {
		return "", err
	}
	if ok {
		return linkedSite(ctx, c, o.dir, proj)
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

func linkedSite(ctx context.Context, c *client.Client, dir string, p project) (string, error) {
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
