package service

import (
	"context"

	"github.com/b0balYson/dropsite/internal/domain"
)

// SiteAttachments is what is connected to a site besides its files, so an
// assistant in a new chat sees the whole picture. Access flags, the expiry
// and own domains are part of SiteView.
type SiteAttachments struct {
	SiteID string
	// FormNames are the forms that received submissions still stored.
	FormNames []string
	// Channels counts delivery channels; WorkingChannels those confirmed,
	// enabled and not paused.
	Channels        int
	WorkingChannels int
	RequireAltcha   bool
	Counters        SiteCounters
	// Collections are the site's data collections with document counts.
	Collections []AttachedCollection
	// ProxyEndpoints are the names of the site's API key proxy endpoints.
	ProxyEndpoints []string
}

// AttachedCollection is one data collection of a site in SiteAttachments.
type AttachedCollection struct {
	Name      string
	Mode      domain.CollectionMode
	Documents int
	Pending   int
}

// SiteAttachments returns what is attached to a site the actor may see.
func (s *Service) SiteAttachments(ctx context.Context, a Actor, ref string) (SiteAttachments, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteAttachments{}, err
	}
	names, err := s.repo.ListFormNames(ctx, site.ID)
	if err != nil {
		return SiteAttachments{}, err
	}
	chs, err := s.repo.ListFormChannels(ctx, site.ID)
	if err != nil {
		return SiteAttachments{}, err
	}
	settings, err := s.repo.GetFormSiteSettings(ctx, site.ID)
	if err != nil {
		return SiteAttachments{}, err
	}
	own, err := s.repo.GetSiteCounters(ctx, site.ID)
	if err != nil {
		return SiteAttachments{}, err
	}
	eff, err := s.settings(ctx)
	if err != nil {
		return SiteAttachments{}, err
	}
	cols, err := s.repo.ListCollections(ctx, site.ID)
	if err != nil {
		return SiteAttachments{}, err
	}
	eps, err := s.repo.ListProxyEndpoints(ctx, site.ID)
	if err != nil {
		return SiteAttachments{}, err
	}
	out := SiteAttachments{
		SiteID: site.ID, FormNames: names, Channels: len(chs), RequireAltcha: settings.RequireAltcha,
		Counters: SiteCounters{Own: own, Global: eff.counters},
	}
	if out.FormNames == nil {
		out.FormNames = []string{}
	}
	out.Collections = make([]AttachedCollection, 0, len(cols))
	for _, c := range cols {
		out.Collections = append(out.Collections,
			AttachedCollection{Name: c.Name, Mode: c.Mode, Documents: c.Documents, Pending: c.Pending})
	}
	out.ProxyEndpoints = make([]string, 0, len(eps))
	for _, ep := range eps {
		out.ProxyEndpoints = append(out.ProxyEndpoints, ep.Name)
	}
	for _, ch := range chs {
		if channelWorks(ch) {
			out.WorkingChannels++
		}
	}
	return out, nil
}

// channelWorks reports whether a channel delivers submissions now.
func channelWorks(ch domain.FormChannel) bool {
	return ch.Status == domain.ChannelActive && ch.Enabled && ch.PausedAt == nil
}
