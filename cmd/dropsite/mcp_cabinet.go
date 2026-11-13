package main

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Account and site settings tools of the local server, through the API.
// The server makes every access decision; the root domain needs the same
// confirmation as on the remote server.

var _ mcp.Cabinet = localBackend{}

func (b localBackend) GetAccount(ctx context.Context) (mcp.Account, error) {
	me, err := b.c.Me(ctx)
	if err != nil {
		return mcp.Account{}, toolError(err)
	}
	lim := me.Limits
	a := mcp.Account{
		Email: me.User.Email, Role: me.User.Role, IsAdmin: me.User.Role == "admin", SitesUsed: me.SiteCount,
		SitesLimit: lim.MaxSitesPerUser, MaxSiteBytes: lim.MaxSiteBytes, MaxSiteSize: mcp.HumanBytes(lim.MaxSiteBytes),
		MaxFileBytes: lim.MaxFileBytes, MaxFileSize: mcp.HumanBytes(lim.MaxFileBytes), MaxFiles: lim.MaxFiles,
		KeepReleases: lim.KeepReleases, MaxDomainsPerSite: me.MaxDomainsPerSite,
		DeployFilesMaxBytes: me.MaxInlineBytes, CabinetURL: me.UIURL,
	}
	if me.RootSite != nil {
		s := fromSite(*me.RootSite)
		a.RootSite = &s
	}
	// Servers before the release check have no /updates: the account is
	// reported without it.
	if u, err := b.c.Updates(ctx, false); err == nil && u.UpdateAvailable {
		a.UpdateAvailable, a.LatestVersion, a.ReleaseNotesURL = true, u.LatestVersion, u.ReleaseNotesURL
	}
	return a, nil
}

// CheckUpdates reports the version facts through the API; it never
// requests an update.
func (b localBackend) CheckUpdates(ctx context.Context, refresh bool) (mcp.UpdateFacts, error) {
	u, err := b.c.Updates(ctx, refresh)
	if err != nil {
		return mcp.UpdateFacts{}, toolError(err)
	}
	out := mcp.UpdateFacts{
		Current: u.CurrentVersion, Latest: u.LatestVersion, Available: u.UpdateAvailable,
		ReleaseURL: u.ReleaseNotesURL, CheckEnabled: u.CheckEnabled, IsAdmin: u.IsAdmin, PanelURL: u.UpdatePanelURL,
	}
	if u.HelperInstalled != nil {
		out.HelperPresent = *u.HelperInstalled
	}
	if u.CheckedAt != nil {
		out.CheckedAt = u.CheckedAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

func (b localBackend) UpdateSite(ctx context.Context, ref string, title, slug *string) (mcp.Site, error) {
	s, err := b.c.UpdateSite(ctx, ref, client.UpdateSiteRequest{Title: title, Slug: slug})
	if err != nil {
		return mcp.Site{}, toolError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) SetSiteAccess(ctx context.Context, ref string, c mcp.AccessChange) (mcp.SiteAccess, error) {
	u := client.UpdateSiteRequest{Noindex: c.HiddenFromSearch, Password: c.Password}
	if c.RemovePassword {
		off := false
		u.Protected = &off
	}
	switch {
	case c.ExpiresAt != nil:
		u.ExpiresAt = &client.Expiry{At: c.ExpiresAt}
	case c.RemoveExpiry:
		u.ExpiresAt = &client.Expiry{}
	}
	var s *client.Site
	var err error
	if u == (client.UpdateSiteRequest{}) {
		// Nothing changes: report the current state.
		s, err = b.c.GetSite(ctx, ref)
	} else {
		s, err = b.c.UpdateSite(ctx, ref, u)
	}
	if err != nil {
		return mcp.SiteAccess{}, toolError(err)
	}
	return mcp.NewSiteAccess(s.ID, s.URL, s.Noindex, s.Protected, s.ExpiresAt), nil
}

// hostURL is the site's address on host, with the scheme and port of the
// site's primary address.
func hostURL(siteURL, host string) string {
	u, err := url.Parse(siteURL)
	if err != nil || u.Host == "" {
		return "https://" + host + "/"
	}
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	return u.Scheme + "://" + host + "/"
}

func toDomain(siteURL string, h client.Hostname) mcp.Domain {
	return mcp.NewDomain(h.Hostname, h.Status, deref(h.Reason), hostURL(siteURL, h.Hostname), h.CreatedAt)
}

func (b localBackend) dnsSetup(ctx context.Context, siteID string) (mcp.DNSSetup, int, error) {
	setup, err := b.c.DomainSetup(ctx, siteID)
	if err != nil {
		return mcp.DNSSetup{}, 0, toolError(err)
	}
	return mcp.NewDNSSetup(setup.CNAMETarget, setup.ServerIPs), setup.MaxDomains, nil
}

func (b localBackend) ListDomains(ctx context.Context, ref string) (mcp.DomainList, error) {
	s, err := b.c.GetSite(ctx, ref)
	if err != nil {
		return mcp.DomainList{}, toolError(err)
	}
	dns, maxDomains, err := b.dnsSetup(ctx, s.ID)
	if err != nil {
		return mcp.DomainList{}, err
	}
	out := mcp.DomainList{SiteID: s.ID, Domains: []mcp.Domain{}, MaxDomains: maxDomains, DNS: dns}
	for _, h := range s.Hostnames {
		if h.Kind == "external" {
			out.Domains = append(out.Domains, toDomain(s.URL, h))
		}
	}
	return out, nil
}

func (b localBackend) AddDomain(ctx context.Context, ref, name string) (mcp.DomainAdded, error) {
	s, err := b.c.GetSite(ctx, ref)
	if err != nil {
		return mcp.DomainAdded{}, toolError(err)
	}
	h, err := b.c.AddHostname(ctx, s.ID, name)
	if err != nil {
		return mcp.DomainAdded{}, toolError(err)
	}
	dns, _, err := b.dnsSetup(ctx, s.ID)
	if err != nil {
		return mcp.DomainAdded{}, err
	}
	return mcp.DomainAdded{SiteID: s.ID, Domain: toDomain(s.URL, *h), DNS: dns}, nil
}

func (b localBackend) RemoveDomain(ctx context.Context, ref, name string) error {
	return toolErrorFor(b.c.RemoveHostname(ctx, ref, name), mcp.NotFoundDomain)
}

func namedViews(in []client.StatsCount) []mcp.NamedViews {
	out := make([]mcp.NamedViews, 0, len(in))
	for _, n := range in {
		out = append(out, mcp.NamedViews{Name: n.Name, Views: n.Views})
	}
	return out
}

func (b localBackend) GetSiteStats(ctx context.Context, ref string, days int) (mcp.SiteStats, error) {
	st, err := b.c.SiteStats(ctx, ref, days)
	if err != nil {
		return mcp.SiteStats{}, toolError(err)
	}
	daily := make([]mcp.DayViews, 0, len(st.Days))
	for _, d := range st.Days {
		daily = append(daily, mcp.DayViews{Date: d.Date, Views: d.Views, Visitors: d.Visitors})
	}
	return mcp.NewStats(st.SiteID, st.From, st.To, st.Totals.Views, st.Totals.Visitors, daily,
		namedViews(st.TopPages), namedViews(st.TopReferrers)), nil
}

func toCounters(c *client.Counters) mcp.Counters {
	return mcp.Counters{SiteID: c.SiteID, Own: mcp.CounterIDs(c.Own), Global: mcp.CounterIDs(c.Global),
		NewDeployID: deref(c.NewDeployID)}
}

func (b localBackend) GetCounters(ctx context.Context, ref string) (mcp.Counters, error) {
	c, err := b.c.Counters(ctx, ref)
	if err != nil {
		return mcp.Counters{}, toolError(err)
	}
	return toCounters(c), nil
}

func (b localBackend) SetCounters(ctx context.Context, ref string, metrica, ga *string) (mcp.Counters, error) {
	c, err := b.c.UpdateCounters(ctx, ref, client.CountersUpdate{YandexMetricaID: metrica, GoogleAnalyticsID: ga})
	if err != nil {
		return mcp.Counters{}, toolError(err)
	}
	return toCounters(c), nil
}

func (b localBackend) ListConnections(ctx context.Context) ([]mcp.Connection, error) {
	l, err := b.c.ListConnections(ctx)
	if err != nil {
		return nil, toolError(err)
	}
	out := make([]mcp.Connection, 0, len(l.Items))
	for _, c := range l.Items {
		id, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, mcp.Connection{
			Kind: c.Kind, ID: id, Name: c.Name, Detail: deref(c.Detail), ConnectedAt: stampTime(&c.ConnectedAt),
			LastUsedAt: stampTime(c.LastUsedAt), ExpiresAt: stampTime(c.ExpiresAt), Current: c.Current,
		})
	}
	return out, nil
}

func (b localBackend) RevokeConnection(ctx context.Context, kind string, id int64) error {
	return toolErrorFor(b.c.RevokeConnection(ctx, kind, strconv.FormatInt(id, 10)), mcp.NotFoundConnection)
}

func (b localBackend) SetRootSite(ctx context.Context, ref, confirm string) (mcp.RootSite, error) {
	r, err := b.c.SetRootSite(ctx, ref, confirm)
	if err != nil {
		return mcp.RootSite{}, toolError(err)
	}
	return mcp.RootSite{RootURL: r.RootURL, Site: fromSite(r.Site)}, nil
}

func (b localBackend) ClearRootSite(ctx context.Context, confirm string) (mcp.RootSite, error) {
	r, err := b.c.ClearRootSite(ctx, confirm)
	if err != nil {
		return mcp.RootSite{}, toolError(err)
	}
	return mcp.RootSite{RootURL: r.RootURL, Site: fromSite(r.Site)}, nil
}
