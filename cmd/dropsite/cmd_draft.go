package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
)

// Draft releases (change staged-publish): 'deploy --draft' saves one,
// 'draft publish' and 'draft discard' finish it.

func cmdDraft(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("draft", "publish|discard <site> [--json]",
		"Make the draft of a site (ID or slug) live, or delete it. A draft is saved with\n"+
			"'"+brand.Name+" deploy --draft'; visitors do not see it until it is published.")
	asJSON := fs.Bool("json", false, "print the API response as JSON")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 || (pos[0] != "publish" && pos[0] != "discard") {
		return usagef("usage: %s draft publish|discard <site>", brand.Name)
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	ref := pos[1]
	if pos[0] == "discard" {
		s, err := c.DiscardDraft(ctx, ref)
		if err != nil {
			return siteError(ref, err)
		}
		if *asJSON {
			return writeJSON(a.stdout, s)
		}
		fmt.Fprintf(a.stdout, "Draft deleted; %s is unchanged.\n%s\n", s.Title, s.URL)
		return nil
	}
	res, err := c.PublishDraft(ctx, ref)
	if err != nil {
		return siteError(ref, err)
	}
	if *asJSON {
		return writeJSON(a.stdout, deployOutput{SiteID: res.Site.ID, DeployID: res.DeployID, URL: res.URL, URLs: res.URLs})
	}
	fmt.Fprintf(a.stdout, "Draft %s is live\n%s\n", res.DeployID, res.URL)
	return nil
}

// printDraft reports a release saved as a draft: the preview address on
// stdout (like the site URL of a publication), guidance on stderr.
func (a *app) printDraft(res *client.DeployResult, preview string) error {
	if preview == "" {
		fmt.Fprintf(a.stderr, "Saved as a draft; this site has no preview address. Visitors still see %s.\n", res.URL)
	} else {
		fmt.Fprintf(a.stderr, "Saved as a draft. Visitors still see %s; preview it at the address below.\n", res.URL)
		fmt.Fprintln(a.stdout, preview)
	}
	fmt.Fprintf(a.stderr, "Make it live: %s draft publish %s\n", brand.Name, res.Site.ID)
	return nil
}
