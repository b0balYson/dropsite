package remote

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"golang.org/x/net/idna"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.Cabinet = backend{}

// siteExtras are the access flags and own domains of a site.
func siteExtras(site domain.Site, hosts []domain.Hostname) mcp.SiteExtras {
	x := mcp.SiteExtras{HiddenFromSearch: site.Noindex, PasswordProtected: site.Protected, ExpiresAt: stamp(site.ExpiresAt)}
	for _, h := range hosts {
		switch h.Kind {
		case domain.HostApex:
			x.RootDomain = true
		case domain.HostExternal:
			x.OwnDomains = append(x.OwnDomains, mcp.DomainBrief{Domain: h.Hostname, Status: string(h.Status())})
		}
	}
	return x
}

// lastDay is the last day a site with this expiry works (the expiry is the
// first instant after that day).
func lastDay(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Add(-time.Nanosecond).Format(time.DateOnly)
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
	return mcp.SiteAccess{
		SiteID: v.Site.ID, URL: v.URL, HiddenFromSearch: v.Site.Noindex, PasswordProtected: v.Site.Protected,
		ExpiresAt: stamp(v.Site.ExpiresAt), LastDay: lastDay(v.Site.ExpiresAt),
	}, nil
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
	d := mcp.DNSSetup{
		Subdomain:      []mcp.DNSRecord{{Type: "CNAME", Name: "<your subdomain, e.g. shop.example.com>", Value: setup.CNAMETarget}},
		RootDomain:     []mcp.DNSRecord{},
		ServerIPsKnown: len(setup.ServerIPs) > 0,
	}
	for _, ip := range setup.ServerIPs {
		d.RootDomain = append(d.RootDomain, mcp.DNSRecord{Type: recordType(ip), Name: "<your domain, e.g. example.com>", Value: ip.String()})
	}
	d.Steps = "At the company where the domain is registered (its DNS settings), create a CNAME record pointing " +
		"to " + setup.CNAMETarget + " for a subdomain such as shop.example.com"
	if d.ServerIPsKnown {
		d.Steps += ", or for a root domain such as example.com the A/AAAA records from for_root_domain"
	} else {
		d.Steps += "; for a root domain such as example.com ask the administrator for the server's IP address " +
			"and create an A record with it"
	}
	d.Steps += ". DNS changes can take up to a few hours. The domain starts opening the site after an " +
		"administrator approves it; HTTPS is set up automatically on the first visit."
	return d
}

func recordType(ip netip.Addr) string {
	if ip.Is4() {
		return "A"
	}
	return "AAAA"
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
		"not found: no such site among this user's sites, or the domain is not attached to it; call list_domains")
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
	out := mcp.SiteStats{
		SiteID: st.SiteID, From: st.From, To: st.To, TotalViews: st.Views, TotalVisitors: st.Visitors,
		Daily: make([]mcp.DayViews, 0, len(st.Days)), TopPages: namedViews(st.TopPages), TopSources: namedViews(st.TopReferrers),
	}
	for _, d := range st.Days {
		dv := mcp.DayViews{Date: d.Day, Views: d.Views, Visitors: d.Visitors}
		out.Daily = append(out.Daily, dv)
		if d.Views > 0 && (out.BusiestDay == nil || d.Views > out.BusiestDay.Views) {
			out.BusiestDay = &dv
		}
	}
	return out, nil
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

const errNoConnection = "not found: no such active connection; call list_connections to see valid kinds and IDs"

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
	return b.notFound(err, errNoConnection)
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
