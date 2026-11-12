package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/client"
)

func cmdClone(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("clone", "<site> [--title <name>] [--slug <address>] [--json]",
		"Create a new site from the current release of a site (ID or slug). Only the files are copied:\n"+
			"forms, domains, the address, password, lifetime and counters stay with the original.")
	title := fs.String("title", "", "name of the new site (default: the original name with \"(copy)\")")
	slug := fs.String("slug", "", "memorable address of the new site")
	asJSON := fs.Bool("json", false, "print one JSON object {site_id, deploy_id, url, urls}")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 1, 1); err != nil {
		return err
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	res, err := c.CloneSite(ctx, pos[0], client.NewSiteRequest{Title: *title, Slug: *slug})
	if err != nil {
		return siteError(pos[0], err)
	}
	if *asJSON {
		return writeJSON(a.stdout, deployOutput{SiteID: res.Site.ID, DeployID: res.DeployID, URL: res.URL, URLs: res.URLs})
	}
	fmt.Fprintf(a.stdout, "Copied to new site %s\n%s\n", res.Site.ID, res.URL)
	return nil
}
