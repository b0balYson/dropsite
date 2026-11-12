package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/b0balYson/dropsite/internal/client"
)

func cmdDelete(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("delete", "<site> [--yes]",
		"Delete a site (ID or slug) with all its releases. Asks to type the site's slug\n"+
			"or ID unless --yes is given; without a terminal --yes is required.")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 1, 1); err != nil {
		return err
	}
	ref := pos[0]
	if !*yes && !a.isTerminal() {
		return withHint(fmt.Errorf("refusing to delete %s without confirmation: stdin is not a terminal", ref),
			"Pass --yes to delete non-interactively.")
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	if !*yes {
		if ref, err = a.confirmDelete(ctx, c, ref); err != nil {
			return err
		}
	}
	if err := c.DeleteSite(ctx, ref); err != nil {
		return siteError(ref, err)
	}
	fmt.Fprintf(a.stdout, "Deleted site %s\n", ref)
	return nil
}

// confirmDelete shows the site and asks for its slug or ID; it returns the ID.
func (a *app) confirmDelete(ctx context.Context, c *client.Client, ref string) (string, error) {
	s, err := c.GetSite(ctx, ref)
	if err != nil {
		return "", siteError(ref, err)
	}
	fmt.Fprintf(a.stderr, "This permanently deletes site %q (%s) and all its releases.\n", s.Title, s.URL)
	fmt.Fprint(a.stderr, "Type the site slug or ID to confirm: ")
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	answer := strings.TrimSpace(line)
	if answer == "" && err != nil {
		return "", fmt.Errorf("read confirmation: %w", err)
	}
	if answer != s.ID && (s.Slug == nil || answer != *s.Slug) {
		return "", errors.New("confirmation did not match, nothing was deleted")
	}
	return s.ID, nil
}
