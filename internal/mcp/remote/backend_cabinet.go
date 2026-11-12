package remote

import (
	"context"
	"errors"

	"golang.org/x/net/idna"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.Cabinet = backend{}

// siteExtras are the access flags and own domains of a site.
func siteExtras(site domain.Site, hosts []domain.Hostname) mcp.SiteExtras {
	hs := make([]mcp.Host, 0, len(hosts))
	for _, h := range hosts {
		hs = append(hs, mcp.Host{Name: h.Hostname, Kind: string(h.Kind), Status: string(h.Status())})
	}
	return mcp.NewSiteExtras(site.Noindex, site.Protected, site.ExpiresAt, hs)
}

func (b backend) GetAccount(ctx context.Context) (mcp.Account, error) {
	u, lim, n, err := b.svc.Me(ctx, b.actor)
	if err != nil {
		return mcp.Account{}, b.toolError(err)
	}
	cfg := b.svc.Config()
	a := mcp.Account{
		Email: u.Email, Role: string(u.Role), IsAdmin: u.IsAdmin(), SitesUsed: n, SitesLimit: lim.MaxSitesPerUser,
		MaxSiteBytes: lim.MaxSiteBytes, MaxSiteSize: mcp.HumanBytes(lim.MaxSiteBytes),
		MaxFileBytes: lim.MaxFileBytes, MaxFileSize: mcp.HumanBytes(lim.MaxFileBytes), MaxFiles: lim.MaxFiles,
		KeepReleases: lim.KeepReleases, MaxDomainsPerSite: cfg.MaxDomainsPerSite,
		DeployFilesMaxBytes: service.MaxInlineBytes, CabinetURL: cfg.UIURL,
	}
	if u.IsAdmin() {
		v, ok, err := b.svc.RootSite(ctx, b.actor)
		if err != nil {
			return mcp.Account{}, b.toolError(err)
		}
		if ok {
			s := toSite(v)
			a.RootSite = &s
		}
	}
	return a, nil
}

func (b backend) UpdateSite(ctx context.Context, ref string, title, slug *string) (mcp.Site, error) {
	v, err := b.svc.UpdateSite(ctx, b.actor, ref, service.SiteUpdate{Title: title, Slug: slug})
	if err != nil {
		return mcp.Site{}, b.toolError(err)
	}
	return toSite(v), nil
}

func (b backend) SetSiteAccess(ctx context.Context, ref string, c mcp.AccessChange) (mcp.SiteAccess, error) {
	u := service.SiteUpdate{Noindex: c.HiddenFromSearch, Password: c.Password}
	if c.RemovePassword {
		off := false
		u.Protected = &off
	}
	switch {
	case c.ExpiresAt != nil:
		u.Expiry = &service.Expiry{At: c.ExpiresAt}
	case c.RemoveExpiry:
		u.Expiry = &service.Expiry{}
	}
	v, err := b.svc.UpdateSite(ctx, b.actor, ref, u)
	if err != nil {
		return mcp.SiteAccess{}, b.toolError(err)
	}
	return mcp.NewSiteAccess(v.Site.ID, v.URL, v.Site.Noindex, v.Site.Protected, v.Site.ExpiresAt), nil
}

func (b backend) toDomain(h domain.Hostname) mcp.Domain {
	d := mcp.Domain{
		Domain: h.Hostname, Status: string(h.Status()), RejectReason: h.RejectReason,
		URL: b.svc.SiteURL(h.Hostname), AddedAt: stamp(&h.CreatedAt),
	}
	if u, err := idna.Display.ToUnicode(h.Hostname); err == nil && u != h.Hostname {
		d.DisplayName = u
	}
	return d
}

// dnsSetup turns the service's DNS targets into records and plain steps.
func dnsSetup(setup service.DomainSetup) mcp.DNSSetup {
	ips := make([]string, 0, len(setup.ServerIPs))
	for _, ip := range setup.ServerIPs {
		ips = append(ips, ip.String())
	}
	return mcp.NewDNSSetup(setup.CNAMETarget, ips)
}

func (b backend) ListDomains(ctx context.Context, ref string) (mcp.DomainList, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.DomainList{}, b.toolError(err)
	}
	setup, err := b.svc.DomainSetup(ctx, b.actor, v.Site.ID)
	if err != nil {
		return mcp.DomainList{}, b.toolError(err)
	}
	out := mcp.DomainList{SiteID: v.Site.ID, Domains: []mcp.Domain{}, MaxDomains: setup.MaxDomains, DNS: dnsSetup(setup)}
	for _, h := range v.Hostnames {
		if h.Kind == domain.HostExternal {
			out.Domains = append(out.Domains, b.toDomain(h))
		}
	}
	return out, nil
}

func (b backend) AddDomain(ctx context.Context, ref, name string) (mcp.DomainAdded, error) {
	h, err := b.svc.AddDomain(ctx, b.actor, ref, name)
	if err != nil {
		return mcp.DomainAdded{}, b.toolError(err)
	}
	setup, err := b.svc.DomainSetup(ctx, b.actor, h.SiteID)
	if err != nil {
		return mcp.DomainAdded{}, b.toolError(err)
	}
	return mcp.DomainAdded{SiteID: h.SiteID, Domain: b.toDomain(h), DNS: dnsSetup(setup)}, nil
}

// notFound replaces the generic "no such site" message for errors about
// other objects.
func (b backend) notFound(err error, msg string) error {
	if err != nil && domain.AsError(err).Kind == domain.KindNotFound {
		return errors.New(msg)
	}
	return b.toolError(err)
}

func (b backend) RemoveDomain(ctx context.Context, ref, name string) error {
	return b.notFound(b.svc.RemoveDomain(ctx, b.actor, ref, name),
		mcp.NotFoundDomain)
}

func namedViews(in []domain.NamedCount) []mcp.NamedViews {
	out := make([]mcp.NamedViews, 0, len(in))
	for _, n := range in {
		out = append(out, mcp.NamedViews{Name: n.Name, Views: n.Views})
	}
	return out
}

func (b backend) GetSiteStats(ctx context.Context, ref string, days int) (mcp.SiteStats, error) {
	st, err := b.svc.GetSiteStats(ctx, b.actor, ref, days)
	if err != nil {
		return mcp.SiteStats{}, b.toolError(err)
	}
	daily := make([]mcp.DayViews, 0, len(st.Days))
	for _, d := range st.Days {
		daily = append(daily, mcp.DayViews{Date: d.Day, Views: d.Views, Visitors: d.Visitors})
	}
	return mcp.NewStats(st.SiteID, st.From, st.To, st.Views, st.Visitors, daily,
		namedViews(st.TopPages), namedViews(st.TopReferrers)), nil
}

func counterIDs(c domain.Counters) mcp.CounterIDs {
	return mcp.CounterIDs{YandexMetricaID: c.MetrikaID, GoogleAnalyticsID: c.GA4ID}
}

func (b backend) counters(ctx context.Context, ref string) (service.SiteCounters, string, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return service.SiteCounters{}, "", err
	}
	c, err := b.svc.GetSiteCounters(ctx, b.actor, v.Site.ID)
	return c, v.Site.ID, err
}

func (b backend) GetCounters(ctx context.Context, ref string) (mcp.Counters, error) {
	c, id, err := b.counters(ctx, ref)
	if err != nil {
		return mcp.Counters{}, b.toolError(err)
	}
	return mcp.Counters{SiteID: id, Own: counterIDs(c.Own), Global: counterIDs(c.Global)}, nil
}

func (b backend) SetCounters(ctx context.Context, ref string, metrica, ga *string) (mcp.Counters, error) {
	cur, id, err := b.counters(ctx, ref)
	if err != nil {
		return mcp.Counters{}, b.toolError(err)
	}
	next := cur.Own
	if metrica != nil {
		next.MetrikaID = *metrica
	}
	if ga != nil {
		next.GA4ID = *ga
	}
	d, err := b.svc.SetSiteCounters(ctx, b.actor, id, next, domain.SourceMCP)
	if err != nil {
		return mcp.Counters{}, b.toolError(err)
	}
	saved, _, err := b.counters(ctx, id)
	if err != nil {
		return mcp.Counters{}, b.toolError(err)
	}
	out := mcp.Counters{SiteID: id, Own: counterIDs(saved.Own), Global: counterIDs(saved.Global)}
	if d != nil {
		out.NewDeployID = d.ID
	}
	return out, nil
}

func (b backend) ListConnections(ctx context.Context) ([]mcp.Connection, error) {
	apps, err := b.svc.ListOAuthGrants(ctx, b.actor)
	if err != nil {
		return nil, b.toolError(err)
	}
	tokens, err := b.svc.ListTokens(ctx, b.actor)
	if err != nil {
		return nil, b.toolError(err)
	}
	out := make([]mcp.Connection, 0, len(apps)+len(tokens))
	for _, g := range apps {
		out = append(out, mcp.Connection{
			Kind: mcp.ConnApp, ID: g.ID, Name: g.ClientName, Detail: g.RedirectHost,
			ConnectedAt: stamp(&g.CreatedAt), LastUsedAt: stamp(g.LastUsedAt), Current: g.ID == b.actor.GrantID,
		})
	}
	now := b.svc.Now()
	for _, t := range tokens {
		if !t.Active(now) {
			continue
		}
		out = append(out, mcp.Connection{
			Kind: mcp.ConnToken, ID: t.ID, Name: t.Name, ConnectedAt: stamp(&t.CreatedAt),
			LastUsedAt: stamp(t.LastUsedAt), ExpiresAt: stamp(t.ExpiresAt), Current: t.ID == b.actor.TokenID,
		})
	}
	return out, nil
}


func (b backend) RevokeConnection(ctx context.Context, kind string, id int64) error {
	var err error
	switch kind {
	case mcp.ConnApp:
		err = b.svc.RevokeOAuthGrant(ctx, b.actor, id)
	case mcp.ConnToken:
		err = b.svc.RevokeToken(ctx, b.actor, id)
	default:
		err = domain.ErrNotFound
	}
	return b.notFound(err, mcp.NotFoundConnection)
}

func (b backend) rootURL() string { return b.svc.SiteURL(b.svc.Config().BaseDomain) }

func (b backend) SetRootSite(ctx context.Context, ref, confirm string) (mcp.RootSite, error) {
	v, err := b.svc.SetRootSite(ctx, b.actor, ref, confirm)
	if err != nil {
		return mcp.RootSite{}, b.toolError(err)
	}
	return mcp.RootSite{RootURL: b.rootURL(), Site: toSite(v)}, nil
}

func (b backend) ClearRootSite(ctx context.Context, confirm string) (mcp.RootSite, error) {
	v, err := b.svc.ClearRootSite(ctx, b.actor, confirm)
	if err != nil {
		return mcp.RootSite{}, b.toolError(err)
	}
	return mcp.RootSite{RootURL: b.rootURL(), Site: toSite(v)}, nil
}
