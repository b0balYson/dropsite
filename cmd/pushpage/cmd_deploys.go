package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/client"
)

func cmdDeploys(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("deploys", "<site> [--json]",
		"Show the releases of a site (ID or slug), newest first; * marks the current one.")
	asJSON := fs.Bool("json", false, "print a JSON array of deploys")
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
	list, err := c.ListDeploys(ctx, pos[0])
	if err != nil {
		return siteError(pos[0], err)
	}
	items := list.Items
	if items == nil {
		items = []client.Deploy{}
	}
	if *asJSON {
		return writeJSON(a.stdout, items)
	}
	return a.printDeploys(items)
}

func (a *app) printDeploys(deploys []client.Deploy) error {
	if len(deploys) == 0 {
		fmt.Fprintln(a.stderr, "No deploys yet.")
		return nil
	}
	tw := newTable(a.stdout)
	fmt.Fprintln(tw, " \tID\tCREATED\tFILES\tSIZE\tSOURCE\tSTATUS")
	for _, d := range deploys {
		mark := " "
		if d.Current {
			mark = "*"
		}
		created := d.CreatedAt
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", mark, d.ID, formatTime(&created),
			d.FileCount, formatBytes(d.SizeBytes), d.Source, d.Status)
	}
	return tw.Flush()
}
