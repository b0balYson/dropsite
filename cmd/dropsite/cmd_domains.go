package main

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/client"
)

const domainsUsage = "<site> | add <site> <domain> | remove <site> <domain> [--json]"

const domainsHelp = `Manage the own domains of a site (ID or slug).

  domains <site>                    list every address with its status
  domains add <site> <domain>       add a domain such as promo.example.com
  domains remove <site> <domain>    remove a domain in any status

A new domain serves nothing until an administrator approves it. Point it at
the server first: a CNAME to the site's random address for a subdomain, or
A/AAAA records with the server's IP addresses for a root domain.`

func cmdDomains(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("domains", domainsUsage, domainsHelp)
	asJSON := fs.Bool("json", false, "print JSON instead of text")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	// One argument always names a site, so a site with the slug "add" or
	// "remove" can still be listed.
	switch {
	case len(pos) == 1:
	case len(pos) == 3 && (pos[0] == "add" || pos[0] == "remove"):
	default:
		return wantArgs(fs, nil, 1, 0)
	}
	c, err := a.connect()
	if err != nil {
		return err
	}
	switch {
	case len(pos) == 1:
		return a.listDomains(ctx, c, pos[0], *asJSON)
	case pos[0] == "add":
		return a.addDomain(ctx, c, pos[1], pos[2], *asJSON)
	default:
		return a.removeDomain(ctx, c, pos[1], pos[2], *asJSON)
	}
}

func (a *app) listDomains(ctx context.Context, c *client.Client, ref string, asJSON bool) error {
	list, err := c.ListHostnames(ctx, ref)
	if err != nil {
		return siteError(ref, err)
	}
	items := list.Items
	if items == nil {
		items = []client.Hostname{}
	}
	if asJSON {
		return writeJSON(a.stdout, items)
	}
	tw := newTable(a.stdout)
	fmt.Fprintln(tw, "HOSTNAME\tKIND\tSTATUS")
	for _, h := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", h.Hostname, h.Kind, describeStatus(h))
	}
	return tw.Flush()
}

func describeStatus(h client.Hostname) string {
	if h.Reason != nil && *h.Reason != "" {
		return h.Status + ": " + sanitizeCell(*h.Reason)
	}
	return h.Status
}

func (a *app) addDomain(ctx context.Context, c *client.Client, ref, domain string, asJSON bool) error {
	h, err := c.AddHostname(ctx, ref, domain)
	if err != nil {
		return siteError(ref, err)
	}
	if asJSON {
		return writeJSON(a.stdout, h)
	}
	fmt.Fprintf(a.stdout, "Added %s to site %s: %s\n", h.Hostname, ref, h.Status)
	target := ""
	if s, err := c.GetSite(ctx, ref); err == nil {
		for _, sh := range s.Hostnames {
			if sh.Kind == "default" {
				target = sh.Hostname
			}
		}
	}
	if target != "" {
		fmt.Fprintf(a.stderr, "Point it at the server: CNAME %s -> %s (subdomain),\n", h.Hostname, target)
	} else {
		fmt.Fprintf(a.stderr, "Point it at the server: a CNAME to the site's random address (subdomain),\n")
	}
	fmt.Fprintln(a.stderr, "or A/AAAA records with the server's IP addresses (root domain).")
	fmt.Fprintln(a.stderr, "It serves the site after an administrator approves it; check with 'dropsite domains "+ref+"'.")
	return nil
}

func (a *app) removeDomain(ctx context.Context, c *client.Client, ref, domain string, asJSON bool) error {
	if err := c.RemoveHostname(ctx, ref, domain); err != nil {
		return siteError(ref, err)
	}
	if asJSON {
		return writeJSON(a.stdout, map[string]any{"hostname": domain, "removed": true})
	}
	fmt.Fprintf(a.stdout, "Removed %s from site %s\n", domain, ref)
	return nil
}
