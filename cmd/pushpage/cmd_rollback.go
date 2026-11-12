package main

import (
	"context"
	"fmt"
)

func cmdRollback(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("rollback", "<site> [deploy_id] [--json]",
		"Switch a site (ID or slug) to the given release, or to the previous one.")
	asJSON := fs.Bool("json", false, "print one JSON object {site_id, deploy_id, url, urls}")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 1, 2); err != nil {
		return err
	}
	deployID := ""
	if len(pos) == 2 {
		deployID = pos[1]
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	res, err := c.Rollback(ctx, pos[0], deployID)
	if err != nil {
		return siteError(pos[0], err)
	}
	if *asJSON {
		return writeJSON(a.stdout, deployOutput{SiteID: res.Site.ID, DeployID: res.DeployID, URL: res.URL, URLs: res.URLs})
	}
	fmt.Fprintf(a.stdout, "Switched to deploy %s\n%s\n", res.DeployID, res.URL)
	return nil
}
