package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/client"
)

// sitesPageSize is the API maximum page size.
const sitesPageSize = 200

func cmdSites(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("sites", "[--json]", "List your sites with their addresses and last deploy time.")
	asJSON := fs.Bool("json", false, "print a JSON array of sites")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 0, 0); err != nil {
		return err
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	sites, err := listAllSites(ctx, c)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, sites)
	}
	return a.printSites(sites)
}

func listAllSites(ctx context.Context, c *client.Client) ([]client.SiteItem, error) {
	all := []client.SiteItem{}
	for {
		page, err := c.ListSites(ctx, client.ListOptions{Limit: sitesPageSize, Offset: len(all)})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if len(page.Items) == 0 || len(all) >= page.Total {
			return all, nil
		}
	}
}

func (a *app) printSites(sites []client.SiteItem) error {
	if len(sites) == 0 {
		fmt.Fprintln(a.stderr, "No sites yet. Publish one with 'pushpage deploy <dir>'.")
		return nil
	}
	tw := newTable(a.stdout)
	fmt.Fprintln(tw, "ID\tURL\tTITLE\tLAST DEPLOY")
	for _, s := range sites {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.ID, s.URL, sanitizeCell(s.Title), formatTime(s.LastDeployAt))
	}
	return tw.Flush()
}
