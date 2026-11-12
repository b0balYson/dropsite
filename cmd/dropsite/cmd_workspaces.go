package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/client"
)

func cmdWorkspaces(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("workspaces", "[--json]",
		"List the shared workspaces you belong to. Every member can change every site of a workspace;\n"+
			"create a site in one with 'deploy --workspace <id>' or 'clone --workspace <id>'.")
	asJSON := fs.Bool("json", false, "print a JSON array of workspaces")
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
	l, err := c.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, l.Items)
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(a.stderr, "No workspaces yet. Create one in the web interface.")
		return nil
	}
	tw := newTable(a.stdout)
	fmt.Fprintln(tw, "ID\tNAME\tMEMBERS\tSITES")
	for _, w := range l.Items {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", w.ID, sanitizeCell(w.Name), w.MemberCount, w.SiteCount)
	}
	return tw.Flush()
}

// sharedWith reports whether a site belongs to a shared workspace the
// logged-in user is a member of. Only own memberships count: an
// administrator who sees every site is not a member of every workspace.
func sharedWith(ctx context.Context, c *client.Client, s *client.Site) bool {
	if s.WorkspaceID == nil {
		return false
	}
	l, err := c.ListWorkspaces(ctx)
	if err != nil {
		return false
	}
	for _, w := range l.Items {
		if w.ID == *s.WorkspaceID {
			return true
		}
	}
	return false
}
