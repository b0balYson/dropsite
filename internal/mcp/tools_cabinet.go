package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Account and site settings tools (the web cabinet of a regular user, plus
// the root domain for administrators). Registered when Options.Cabinet is
// set; the backend must implement Cabinet.

// DefaultStatsDays is the period of get_site_stats when days is omitted.
const DefaultStatsDays = 7

type updateSiteArgs struct {
	Site  string  `json:"site" jsonschema:"site ID or slug, as returned by list_sites"`
	Title *string `json:"title,omitempty" jsonschema:"new site name; omit to keep it"`
	Slug  *string `json:"slug,omitempty" jsonschema:"new memorable address label (a-z, 0-9, hyphens); an empty string removes it; omit to keep it"`
}

type accessArgs struct {
	Site             string  `json:"site" jsonschema:"site ID or slug"`
	HiddenFromSearch *bool   `json:"hidden_from_search,omitempty" jsonschema:"true asks search engines not to index the site, false allows indexing; omit to keep"`
	Password         *string `json:"password,omitempty" jsonschema:"protect the site with this visitor password, or change it; omit to keep"`
	RemovePassword   bool    `json:"remove_password,omitempty" jsonschema:"true removes the password: the site opens for everyone"`
	ExpiresOn        *string `json:"expires_on,omitempty" jsonschema:"last day the site works, YYYY-MM-DD (it stops at the end of that day, UTC); omit to keep"`
	RemoveExpiry     bool    `json:"remove_expiry,omitempty" jsonschema:"true removes the expiry: the site works without a time limit"`
}

type domainArgs struct {
	Site   string `json:"site" jsonschema:"site ID or slug"`
	Domain string `json:"domain" jsonschema:"the user's own domain, e.g. shop.example.com or example.com"`
}

type removeDomainArgs struct {
	Site    string `json:"site" jsonschema:"site ID or slug"`
	Domain  string `json:"domain" jsonschema:"the own domain to remove"`
	Confirm string `json:"confirm" jsonschema:"the same domain name typed again after the user explicitly confirmed"`
}

type statsArgs struct {
	Site string `json:"site" jsonschema:"site ID or slug"`
	Days int    `json:"days,omitempty" jsonschema:"number of days up to today, 1 to 400; default 7"`
}

type countersArgs struct {
	Site              string  `json:"site" jsonschema:"site ID or slug"`
	YandexMetricaID   *string `json:"yandex_metrica_id,omitempty" jsonschema:"Yandex Metrica counter number; an empty string removes it; omit to keep"`
	GoogleAnalyticsID *string `json:"google_analytics_id,omitempty" jsonschema:"Google Analytics 4 measurement ID (G-XXXXXXX); an empty string removes it; omit to keep"`
}

type revokeArgs struct {
	Kind    string `json:"kind" jsonschema:"app or token, as returned by list_connections"`
	ID      int64  `json:"id" jsonschema:"connection ID, as returned by list_connections"`
	Confirm string `json:"confirm" jsonschema:"the connection's name (or ID) typed again after the user explicitly confirmed"`
}

type rootArgs struct {
	Site    string `json:"site" jsonschema:"ID or slug of the site to show on the root domain (any user's site)"`
	Confirm string `json:"confirm" jsonschema:"the same site slug or ID typed again after the user explicitly confirmed"`
}

type clearRootArgs struct {
	Confirm string `json:"confirm" jsonschema:"slug or ID of the site now on the root domain, typed again after the user explicitly confirmed"`
}

type removedDomain struct {
	Removed string `json:"removed" jsonschema:"the domain that no longer opens the site"`
	Summary string `json:"summary"`
}

type connectionsOut struct {
	Connections []Connection `json:"connections"`
	Summary     string       `json:"summary"`
}

type revokedOut struct {
	Revoked    Connection `json:"revoked"`
	WasCurrent bool       `json:"was_current" jsonschema:"true when this assistant's own connection was revoked"`
	Summary    string     `json:"summary"`
}

var errNoCabinet = errors.New("this server does not offer account and site settings; use the dropsite web interface")

// cabinet adapts a Cabinet operation to a tool handler.
func cabinet[In, Out any](t tools, fn func(context.Context, Cabinet, In) (Out, error)) sdk.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		var zero Out
		b, err := t.resolve(ctx, req)
		if err != nil {
			return nil, zero, err
		}
		c, ok := b.(Cabinet)
		if !ok {
			return nil, zero, errNoCabinet
		}
		out, err := fn(ctx, c, in)
		if err != nil {
			return nil, zero, err
		}
		return nil, out, nil
	}
}

func (t tools) registerCabinet(s *sdk.Server, o Options) {
	if !o.Cabinet {
		return
	}
	t.registerAccountTools(s)
	t.registerDomainTools(s)
	t.registerInsightTools(s)
	t.registerConnectionTools(s)
	t.registerRootTools(s)
	t.registerProxyTools(s)
}

func (t tools) registerAccountTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "get_account", Annotations: readOnly("Get account"),
		Description: "Show the user's dropsite account: email, role, how many sites they have out of their limit, " +
			"the size limits of a site and the web interface address. Use it when the user asks about their " +
			"account, limits or quota, or before publishing something big. Tell the user the summary in plain words."},
		cabinet(t, func(ctx context.Context, c Cabinet, _ struct{}) (Account, error) {
			a, err := c.GetAccount(ctx)
			if err != nil {
				return Account{}, err
			}
			a.Summary = accountSummary(a)
			return a, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "update_site", Annotations: writes("Update site", false, true),
		Description: "Rename a site and/or change its memorable address (slug, e.g. promo-may.<domain>). Pass only " +
			"what changes; an empty slug removes it and the site keeps its random address. After a slug change " +
			"the old memorable address stops working: tell the user the new link from url."},
		cabinet(t, updateSite))
	sdk.AddTool(s, &sdk.Tool{Name: "set_site_access", Annotations: writes("Set site access", false, true),
		Description: "See or change who can open a site: hide it from search engines, protect it with a visitor " +
			"password (set, change or remove), and set or remove the last day it works. Pass only what changes; " +
			"with only site it just reports the current state. Before setting a password, suggest one the user " +
			"does not use anywhere else (it passes through this chat); never repeat the password back unless " +
			"the user asks. Tell the user the summary and that visitors need the password to open the site."},
		cabinet(t, setAccess))
}

func (t tools) registerDomainTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_domains", Annotations: readOnly("List own domains"),
		Description: "List the user's own domains attached to a site (such as shop.example.com) with their status, " +
			"and the DNS records that point a domain at the site. Use it when the user asks whether their domain " +
			"works or how to set it up. A pending domain waits for an administrator to approve it."},
		cabinet(t, func(ctx context.Context, c Cabinet, in siteArgs) (DomainList, error) {
			l, err := c.ListDomains(ctx, in.Site)
			if err != nil {
				return DomainList{}, err
			}
			l.Summary = domainsSummary(l.Domains)
			return l, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "add_domain", Annotations: writes("Add own domain", false, false),
		Description: "Attach the user's own domain to a site. The user then creates the DNS record from dns at " +
			"their domain provider (registrar) themselves; you cannot do that for them. The domain starts " +
			"working after an administrator approves it, and HTTPS is set up automatically. Explain these steps " +
			"to the user in plain words."},
		cabinet(t, func(ctx context.Context, c Cabinet, in domainArgs) (DomainAdded, error) {
			d, err := c.AddDomain(ctx, in.Site, in.Domain)
			if err != nil {
				return DomainAdded{}, err
			}
			d.Summary = d.Domain.Domain + " was added and waits for an administrator to approve it. " + d.DNS.Steps
			return d, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "remove_domain", Annotations: writes("Remove own domain", true, true),
		Description: "Detach an own domain from a site; it stops opening the site at once. Only after the user " +
			"explicitly confirmed: pass the domain again in confirm."},
		cabinet(t, removeDomain))
}

func (t tools) registerInsightTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "get_site_stats", Annotations: readOnly("Site statistics"),
		Description: "Show visits of a site: views and unique visitors per day, totals, the busiest day, the most " +
			"viewed pages and the sites visitors came from. Default period is the last 7 days (today included, " +
			"UTC); use days=30 for a month. Statistics are updated every few minutes. Tell the user the summary " +
			"and, if helpful, a short table."},
		cabinet(t, func(ctx context.Context, c Cabinet, in statsArgs) (SiteStats, error) {
			days := in.Days
			if days == 0 {
				days = DefaultStatsDays
			}
			st, err := c.GetSiteStats(ctx, in.Site, days)
			if err != nil {
				return SiteStats{}, err
			}
			st.Summary = statsSummary(st)
			return st, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "get_counters", Annotations: readOnly("Get analytics counters"),
		Description: "Show the analytics counters (Yandex Metrica, Google Analytics 4) added to a site's pages, " +
			"including counters an administrator adds to every site."},
		cabinet(t, func(ctx context.Context, c Cabinet, in siteArgs) (Counters, error) {
			out, err := c.GetCounters(ctx, in.Site)
			if err != nil {
				return Counters{}, err
			}
			out.Summary = countersSummary(out)
			return out, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "set_counters", Annotations: writes("Set analytics counters", false, true),
		Description: "Add, change or remove a site's Yandex Metrica counter number or Google Analytics 4 " +
			"measurement ID; pass only what changes. dropsite inserts the counter code into every page, so do " +
			"not add it to the files yourself. A published site gets a new release with the counters."},
		cabinet(t, func(ctx context.Context, c Cabinet, in countersArgs) (Counters, error) {
			if in.YandexMetricaID == nil && in.GoogleAnalyticsID == nil {
				return Counters{}, errors.New("nothing to change: pass yandex_metrica_id and/or google_analytics_id")
			}
			out, err := c.SetCounters(ctx, in.Site, in.YandexMetricaID, in.GoogleAnalyticsID)
			if err != nil {
				return Counters{}, err
			}
			out.Summary = "Saved. " + countersSummary(out)
			if out.NewDeployID != "" {
				out.Summary += " The site was republished with the counters."
			}
			return out, nil
		}))
}

func (t tools) registerConnectionTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_connections", Annotations: readOnly("List connections"),
		Description: "List the apps (such as chat assistants) and API tokens (such as the dropsite CLI) connected " +
			"to the user's account, marking the one this assistant uses. No secrets are shown. New connections " +
			"are made only in the web interface (Connect AI page), never through this chat."},
		cabinet(t, func(ctx context.Context, c Cabinet, _ struct{}) (connectionsOut, error) {
			cs, err := c.ListConnections(ctx)
			if err != nil {
				return connectionsOut{}, err
			}
			if cs == nil {
				cs = []Connection{}
			}
			return connectionsOut{Connections: cs, Summary: connectionsSummary(cs)}, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "revoke_connection", Annotations: writes("Revoke connection", true, true),
		Description: "Disconnect one of the user's apps or API tokens; it stops working at once. Revoking the " +
			"connection marked current disconnects this assistant too: warn the user first. Only after the user " +
			"explicitly confirmed: pass the connection's name (or ID) again in confirm."},
		cabinet(t, revokeConnection))
}

func (t tools) registerRootTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "set_root_site", Annotations: writes("Put site on root domain", true, true),
		Description: "Administrators only: show a site (for example a placeholder page) on the server's root " +
			"domain, replacing the site there now. Other sites keep their addresses. Any user's site may be " +
			"chosen. Only after the user explicitly confirmed: pass the site's slug or ID again in confirm. " +
			"Regular users get a refusal: tell them to ask the administrator."},
		cabinet(t, func(ctx context.Context, c Cabinet, in rootArgs) (RootSite, error) {
			r, err := c.SetRootSite(ctx, in.Site, in.Confirm)
			if err != nil {
				return RootSite{}, err
			}
			r.Summary = fmt.Sprintf("%s now shows %q. Its other addresses keep working.", r.RootURL, r.Site.Title)
			return r, nil
		}))
	sdk.AddTool(s, &sdk.Tool{Name: "clear_root_site", Annotations: writes("Clear root domain", true, true),
		Description: "Administrators only: take the site off the server's root domain, which then shows nothing. " +
			"The site keeps its other addresses. Only after the user explicitly confirmed: pass the slug or ID " +
			"of the site now on the root domain (see get_account) in confirm."},
		cabinet(t, func(ctx context.Context, c Cabinet, in clearRootArgs) (RootSite, error) {
			r, err := c.ClearRootSite(ctx, in.Confirm)
			if err != nil {
				return RootSite{}, err
			}
			r.Summary = fmt.Sprintf("%s no longer shows %q. The site keeps its other addresses.", r.RootURL, r.Site.Title)
			return r, nil
		}))
}

func updateSite(ctx context.Context, c Cabinet, in updateSiteArgs) (Site, error) {
	if in.Title == nil && in.Slug == nil {
		return Site{}, errors.New("nothing to change: pass title and/or slug")
	}
	return c.UpdateSite(ctx, in.Site, in.Title, in.Slug)
}

// expiryLayout is the date format of expires_on.
const expiryLayout = time.DateOnly

func setAccess(ctx context.Context, c Cabinet, in accessArgs) (SiteAccess, error) {
	ch := AccessChange{HiddenFromSearch: in.HiddenFromSearch, Password: in.Password,
		RemovePassword: in.RemovePassword, RemoveExpiry: in.RemoveExpiry}
	if in.Password != nil && in.RemovePassword {
		return SiteAccess{}, errors.New("pass either password or remove_password, not both")
	}
	if in.ExpiresOn != nil && in.RemoveExpiry {
		return SiteAccess{}, errors.New("pass either expires_on or remove_expiry, not both")
	}
	if in.ExpiresOn != nil {
		d, err := time.Parse(expiryLayout, strings.TrimSpace(*in.ExpiresOn))
		if err != nil {
			return SiteAccess{}, errors.New("expires_on must be a date like 2026-12-31 (the last day the site works)")
		}
		end := d.UTC().Add(24 * time.Hour)
		ch.ExpiresAt = &end
	}
	a, err := c.SetSiteAccess(ctx, in.Site, ch)
	if err != nil {
		return SiteAccess{}, err
	}
	a.Summary = accessSummary(a)
	return a, nil
}

// normDomain normalises a domain name for comparison.
func normDomain(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

func removeDomain(ctx context.Context, c Cabinet, in removeDomainArgs) (removedDomain, error) {
	name := normDomain(in.Domain)
	if name == "" || normDomain(in.Confirm) != name {
		return removedDomain{}, errors.New("not removed: ask the user to confirm, then call remove_domain again " +
			"with confirm set to the domain name")
	}
	if err := c.RemoveDomain(ctx, in.Site, name); err != nil {
		return removedDomain{}, err
	}
	return removedDomain{Removed: name, Summary: name + " no longer opens the site. The user may also delete " +
		"its DNS record at the domain provider."}, nil
}

func revokeConnection(ctx context.Context, c Cabinet, in revokeArgs) (revokedOut, error) {
	cs, err := c.ListConnections(ctx)
	if err != nil {
		return revokedOut{}, err
	}
	var target *Connection
	for i := range cs {
		if cs[i].Kind == in.Kind && cs[i].ID == in.ID {
			target = &cs[i]
		}
	}
	if target == nil {
		return revokedOut{}, errors.New("not found: no such active connection; call list_connections to see valid kinds and IDs")
	}
	confirm := strings.ToLower(strings.TrimSpace(in.Confirm))
	if confirm == "" || (confirm != strings.ToLower(strings.TrimSpace(target.Name)) && confirm != strconv.FormatInt(target.ID, 10)) {
		return revokedOut{}, errors.New("not revoked: ask the user to confirm, then call revoke_connection again " +
			"with confirm set to the connection's name exactly as list_connections shows it")
	}
	if err := c.RevokeConnection(ctx, target.Kind, target.ID); err != nil {
		return revokedOut{}, err
	}
	sum := fmt.Sprintf("%q was disconnected and can no longer use the account.", target.Name)
	if target.Current {
		sum += " This was this assistant's own connection: it has no access from now on; the user can reconnect " +
			"on the Connect AI page of the web interface."
	}
	return revokedOut{Revoked: *target, WasCurrent: target.Current, Summary: sum}, nil
}
