package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// SiteView is a site with its addresses and current release, for adapters.
type SiteView struct {
	Site      domain.Site
	OwnerID   int64
	Hostnames []domain.Hostname
	URL       string   // primary URL: slug if set, otherwise default
	URLs      []string // every active address, primary first
	Slug      string
	Current   *domain.Deploy
}

const maxSiteIDAttempts = 5

func (s *Service) host(label string) string { return label + "." + s.cfg.BaseDomain }

func siteTarget(id string) string { return "site:" + id }

// resolveSite finds an active site by ID or slug, visible to the actor.
// Anything the actor may not see is reported exactly like a missing site.
func (s *Service) resolveSite(ctx context.Context, a Actor, ref string) (domain.Site, error) {
	if !domain.IsValidHostname(ref) || len(ref) > 63 {
		return domain.Site{}, domain.ErrNotFound
	}
	site, err := s.repo.GetSite(ctx, ref)
	if errors.Is(err, domain.ErrNotFound) {
		site, err = s.repo.FindSiteBySlug(ctx, ref)
	}
	if err != nil {
		return domain.Site{}, err
	}
	if !a.IsAdmin() && site.OwnerID != a.User.ID {
		return domain.Site{}, domain.ErrNotFound
	}
	return site, nil
}

func (s *Service) view(ctx context.Context, site domain.Site) (SiteView, error) {
	hosts, err := s.repo.ListHostnames(ctx, site.ID)
	if err != nil {
		return SiteView{}, err
	}
	v := SiteView{Site: site, OwnerID: site.OwnerID}
	v.Hostnames, v.URLs, v.Slug = s.orderHosts(hosts)
	if len(v.URLs) > 0 {
		v.URL = v.URLs[0]
	}
	if site.CurrentDeployID != nil {
		d, err := s.repo.GetDeploy(ctx, site.ID, *site.CurrentDeployID)
		if err != nil {
			return SiteView{}, err
		}
		v.Current = &d
	}
	return v, nil
}

// orderHosts puts the primary address first: slug, then default, then apex,
// then external domains. URLs list only active addresses: an external domain
// waiting for approval does not serve the site.
func (s *Service) orderHosts(hosts []domain.Hostname) ([]domain.Hostname, []string, string) {
	rank := map[domain.HostKind]int{domain.HostSlug: 0, domain.HostDefault: 1, domain.HostApex: 2, domain.HostExternal: 3}
	sorted := append([]domain.Hostname(nil), hosts...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank[sorted[i].Kind] < rank[sorted[j].Kind] })
	urls := make([]string, 0, len(sorted))
	slug := ""
	for _, h := range sorted {
		if h.Active() {
			urls = append(urls, s.SiteURL(h.Hostname))
		}
		if h.Kind == domain.HostSlug {
			slug = h.Hostname[:len(h.Hostname)-len(s.cfg.BaseDomain)-1]
		}
	}
	return sorted, urls, slug
}

// SiteSummaryView decorates a list row with URLs.
type SiteSummaryView struct {
	store.SiteSummary
	URL  string
	URLs []string
	Slug string
}

func (s *Service) summaries(rows []store.SiteSummary) []SiteSummaryView {
	out := make([]SiteSummaryView, 0, len(rows))
	for _, r := range rows {
		hosts, urls, slug := s.orderHosts(r.Hostnames)
		r.Hostnames = hosts
		v := SiteSummaryView{SiteSummary: r, URLs: urls, Slug: slug}
		if len(urls) > 0 {
			v.URL = urls[0]
		}
		out = append(out, v)
	}
	return out
}

// ListSites returns the actor's own sites. Admins use ListAllSites for others.
func (s *Service) ListSites(ctx context.Context, a Actor, p store.Page) ([]SiteSummaryView, int, error) {
	owner := a.User.ID
	rows, total, err := s.repo.ListSites(ctx, store.SiteFilter{OwnerID: &owner, Page: p})
	if err != nil {
		return nil, 0, err
	}
	return s.summaries(rows), total, nil
}

// ListAllSites searches every site (admin only).
func (s *Service) ListAllSites(ctx context.Context, a Actor, query string, p store.Page) ([]SiteSummaryView, int, error) {
	if err := requireAdmin(a); err != nil {
		return nil, 0, err
	}
	rows, total, err := s.repo.ListSites(ctx, store.SiteFilter{Query: query, Page: p})
	if err != nil {
		return nil, 0, err
	}
	return s.summaries(rows), total, nil
}

// GetSite returns a site by ID or slug.
func (s *Service) GetSite(ctx context.Context, a Actor, ref string) (SiteView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	return s.view(ctx, site)
}

func (s *Service) checkSlug(ctx context.Context, slug string) error {
	eff, err := s.settings(ctx)
	if err != nil {
		return err
	}
	return domain.ValidateSlug(slug, eff.reserved)
}

// CreateSite creates a site with a random default address and optional slug.
func (s *Service) CreateSite(ctx context.Context, a Actor, title, slug string) (SiteView, error) {
	title, err := domain.ValidateTitle(title)
	if err != nil {
		return SiteView{}, err
	}
	if slug != "" {
		if err := s.checkSlug(ctx, slug); err != nil {
			return SiteView{}, err
		}
	}
	lim, err := s.Limits(ctx)
	if err != nil {
		return SiteView{}, err
	}
	n, err := s.repo.CountActiveSites(ctx, a.User.ID)
	if err != nil {
		return SiteView{}, err
	}
	if n >= lim.MaxSitesPerUser {
		return SiteView{}, domain.E(domain.KindLimit, domain.CodeSiteLimit, "site limit reached").
			WithParams(map[string]any{"limit": lim.MaxSitesPerUser})
	}
	site, hosts, err := s.insertSite(ctx, a, title, slug)
	if err != nil {
		return SiteView{}, err
	}
	if err := s.files.ActivateEmpty(site.ID); err != nil {
		return SiteView{}, fmt.Errorf("prepare site directory: %w", err)
	}
	for _, h := range hosts {
		if err := s.files.LinkHost(h.Hostname, site.ID); err != nil {
			return SiteView{}, fmt.Errorf("link host: %w", err)
		}
	}
	s.audit(ctx, a, "site.create", siteTarget(site.ID), map[string]any{"title": title, "slug": slug})
	return s.view(ctx, site)
}

func (s *Service) insertSite(ctx context.Context, a Actor, title, slug string) (domain.Site, []domain.Hostname, error) {
	now := s.now()
	for range maxSiteIDAttempts {
		id := domain.NewSiteID()
		used, err := s.repo.SiteIDExists(ctx, id)
		if err != nil {
			return domain.Site{}, nil, err
		}
		if used {
			continue
		}
		site := domain.Site{ID: id, OwnerID: a.User.ID, Title: title, CreatedAt: now, UpdatedAt: now}
		hosts := []domain.Hostname{{Hostname: s.host(id), SiteID: id, Kind: domain.HostDefault, Verified: true, CreatedAt: now}}
		if slug != "" {
			hosts = append(hosts, domain.Hostname{Hostname: s.host(slug), SiteID: id, Kind: domain.HostSlug, Verified: true, CreatedAt: now})
		}
		if err := s.repo.CreateSite(ctx, site, hosts); err != nil {
			return domain.Site{}, nil, withSlug(err, slug)
		}
		return site, hosts, nil
	}
	return domain.Site{}, nil, errors.New("could not allocate a unique site id")
}

// SiteUpdate changes a site; nil fields stay unchanged. An empty Slug removes it.
type SiteUpdate struct {
	Title *string
	Slug  *string
}

// UpdateSite changes title and slug.
func (s *Service) UpdateSite(ctx context.Context, a Actor, ref string, u SiteUpdate) (SiteView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	if u.Title != nil {
		title, err := domain.ValidateTitle(*u.Title)
		if err != nil {
			return SiteView{}, err
		}
		if title != site.Title {
			if err := s.repo.UpdateSiteTitle(ctx, site.ID, title, s.now()); err != nil {
				return SiteView{}, err
			}
			s.audit(ctx, a, "site.update", siteTarget(site.ID), map[string]any{"title": title})
		}
	}
	if u.Slug != nil {
		if err := s.setSlug(ctx, a, site, *u.Slug); err != nil {
			return SiteView{}, err
		}
	}
	site, err = s.repo.GetSite(ctx, site.ID)
	if err != nil {
		return SiteView{}, err
	}
	return s.view(ctx, site)
}

func (s *Service) setSlug(ctx context.Context, a Actor, site domain.Site, slug string) error {
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	var host *domain.Hostname
	if slug != "" {
		if err := s.checkSlug(ctx, slug); err != nil {
			return err
		}
		host = &domain.Hostname{Hostname: s.host(slug), SiteID: site.ID, Kind: domain.HostSlug, Verified: true, CreatedAt: s.now()}
	}
	removed, err := s.repo.SetSlugHost(ctx, site.ID, host, s.now())
	if err != nil {
		return withSlug(err, slug)
	}
	if host != nil && removed == host.Hostname {
		return nil // unchanged
	}
	if host != nil {
		if err := s.files.LinkHost(host.Hostname, site.ID); err != nil {
			return fmt.Errorf("link host: %w", err)
		}
	}
	if removed != "" {
		if err := s.files.UnlinkHost(removed); err != nil {
			return fmt.Errorf("unlink host: %w", err)
		}
	}
	s.audit(ctx, a, "site.slug", siteTarget(site.ID), map[string]any{"slug": slug, "removed": removed})
	return nil
}

// SlugStatus answers the UI availability check.
type SlugStatus struct {
	Available bool
	Code      string
}

// CheckSlugAvailable reports whether slug could be set on the given site
// (empty ref means a new site).
func (s *Service) CheckSlugAvailable(ctx context.Context, a Actor, ref, slug string) (SlugStatus, error) {
	if err := s.checkSlug(ctx, slug); err != nil {
		return SlugStatus{Code: domain.AsError(err).Code}, nil
	}
	if ref != "" {
		site, err := s.resolveSite(ctx, a, ref)
		if err != nil {
			return SlugStatus{}, err
		}
		hosts, err := s.repo.ListHostnames(ctx, site.ID)
		if err != nil {
			return SlugStatus{}, err
		}
		for _, h := range hosts {
			if h.Hostname == s.host(slug) {
				return SlugStatus{Available: true}, nil
			}
		}
	}
	if taken, err := s.repo.SiteIDExists(ctx, slug); err != nil {
		return SlugStatus{}, err
	} else if taken {
		return SlugStatus{Code: domain.CodeSlugTaken}, nil
	}
	if _, err := s.repo.FindSiteBySlug(ctx, slug); err == nil {
		return SlugStatus{Code: domain.CodeSlugTaken}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return SlugStatus{}, err
	}
	return SlugStatus{Available: true}, nil
}

// DeleteSite soft-deletes a site: addresses stop working at once and are
// released; files are purged after DeletedSiteTTL.
func (s *Service) DeleteSite(ctx context.Context, a Actor, ref string) error {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	removed, err := s.repo.SoftDeleteSite(ctx, site.ID, s.now())
	if err != nil {
		return err
	}
	for _, h := range removed {
		if err := s.files.UnlinkHost(h); err != nil {
			return fmt.Errorf("unlink host: %w", err)
		}
	}
	s.audit(ctx, a, "site.delete", siteTarget(site.ID), map[string]any{"title": site.Title, "hosts": removed})
	return nil
}

// SetApex binds the base domain root to a site (admin only).
func (s *Service) SetApex(ctx context.Context, a Actor, ref string) (SiteView, error) {
	if err := requireAdmin(a); err != nil {
		return SiteView{}, err
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	host := domain.Hostname{Hostname: s.cfg.BaseDomain, SiteID: site.ID, Kind: domain.HostApex, Verified: true, CreatedAt: s.now()}
	prev, err := s.repo.SetApexHost(ctx, site.ID, host, s.now())
	if err != nil {
		return SiteView{}, err
	}
	if err := s.files.LinkHost(host.Hostname, site.ID); err != nil {
		return SiteView{}, fmt.Errorf("link apex: %w", err)
	}
	s.audit(ctx, a, "site.apex.set", siteTarget(site.ID), map[string]any{"previous_site": prev})
	return s.view(ctx, site)
}

// ClearApex unbinds the base domain root from a site (admin only).
func (s *Service) ClearApex(ctx context.Context, a Actor, ref string) (SiteView, error) {
	if err := requireAdmin(a); err != nil {
		return SiteView{}, err
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	removed, err := s.repo.ClearApexHost(ctx, site.ID, s.now())
	if err != nil {
		return SiteView{}, err
	}
	if removed != "" {
		if err := s.files.UnlinkHost(removed); err != nil {
			return SiteView{}, fmt.Errorf("unlink apex: %w", err)
		}
		s.audit(ctx, a, "site.apex.clear", siteTarget(site.ID), nil)
	}
	return s.view(ctx, site)
}

// TransferSite gives a site to another active user (admin only).
func (s *Service) TransferSite(ctx context.Context, a Actor, ref string, newOwnerID int64) (SiteView, error) {
	if err := requireAdmin(a); err != nil {
		return SiteView{}, err
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	owner, err := s.repo.GetUser(ctx, newOwnerID)
	if err != nil || owner.Status != domain.UserActive {
		return SiteView{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "the new owner must be an active user")
	}
	if err := s.repo.TransferSite(ctx, site.ID, newOwnerID, s.now()); err != nil {
		return SiteView{}, err
	}
	s.audit(ctx, a, "site.transfer", siteTarget(site.ID), map[string]any{"from": site.OwnerID, "to": newOwnerID, "to_email": owner.Email})
	site.OwnerID = newOwnerID
	return s.view(ctx, site)
}

// withSlug attaches the requested slug to a slug_taken error for clients.
func withSlug(err error, slug string) error {
	if de := domain.AsError(err); de.Code == domain.CodeSlugTaken {
		return de.WithParams(map[string]any{"slug": slug})
	}
	return err
}
