package service

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/dnscheck"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// serverIPsTTL is how long discovered server addresses are reused.
const serverIPsTTL = 5 * time.Minute

// serverIPsTimeout bounds discovery so an owner's page never waits long.
const serverIPsTimeout = 2 * time.Second

// DomainView is an external domain with its site and owner (admin screens).
type DomainView = store.ExternalHost

// DomainFilter filters ListDomains.
type DomainFilter = store.ExternalHostFilter

// DomainSetup tells an owner which DNS record to create.
type DomainSetup struct {
	ServerIPs   []netip.Addr // A/AAAA target; empty when unknown
	CNAMETarget string       // the site's default address
	MaxDomains  int
}

// serverIPCache remembers discovered server addresses; failures are not cached.
type serverIPCache struct {
	mu    sync.Mutex
	addrs []netip.Addr
	until time.Time
}

func (s *Service) externalRules() domain.ExternalHostRules {
	uiHost := ""
	if u, err := url.Parse(s.cfg.UIURL); err == nil {
		uiHost = u.Hostname()
	}
	return domain.ExternalHostRules{BaseDomain: s.cfg.BaseDomain, APIHost: s.cfg.APIHost, UIHost: uiHost}
}

// lookupName normalises a hostname given in a path; anything that cannot be
// a stored external domain is reported as not found.
func lookupName(raw string) (string, error) {
	h, err := domain.NormalizeExternalHost(raw, domain.ExternalHostRules{})
	if err != nil {
		return "", domain.ErrNotFound
	}
	return h, nil
}

// ListHostnames returns every address of a site with its status.
func (s *Service) ListHostnames(ctx context.Context, a Actor, ref string) ([]domain.Hostname, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	hosts, err := s.repo.ListHostnames(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	sorted, _, _ := s.orderHosts(hosts)
	return sorted, nil
}

// AddDomain adds an external domain to a site; it waits for an administrator.
func (s *Service) AddDomain(ctx context.Context, a Actor, ref, raw string) (domain.Hostname, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return domain.Hostname{}, err
	}
	name, err := domain.NormalizeExternalHost(raw, s.externalRules())
	if err != nil {
		return domain.Hostname{}, err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	h := domain.Hostname{Hostname: name, SiteID: site.ID, Kind: domain.HostExternal, CreatedAt: s.now()}
	if err := s.repo.AddExternalHost(ctx, h, s.cfg.MaxDomainsPerSite); err != nil {
		if de := domain.AsError(err); de.Code == domain.CodeDomainTaken {
			return domain.Hostname{}, de.WithParams(map[string]any{"domain": name})
		}
		return domain.Hostname{}, err
	}
	s.audit(ctx, a, "domain.add", siteTarget(site.ID), map[string]any{"domain": name})
	return h, nil
}

// RemoveDomain removes an external domain of a site in any status.
func (s *Service) RemoveDomain(ctx context.Context, a Actor, ref, hostname string) error {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return err
	}
	name, err := lookupName(hostname)
	if err != nil {
		return err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	removed, err := s.repo.RemoveExternalHost(ctx, site.ID, name, s.now())
	if err != nil {
		return err
	}
	if err := s.files.UnlinkHost(name); err != nil {
		return fmt.Errorf("unlink domain: %w", err)
	}
	s.audit(ctx, a, "domain.remove", siteTarget(site.ID), map[string]any{
		"domain": name, "status": string(removed.Status()),
	})
	return nil
}

// ListDomains lists external domains of all sites (admin only).
func (s *Service) ListDomains(ctx context.Context, a Actor, f DomainFilter) ([]DomainView, int, error) {
	if err := requireAdmin(a); err != nil {
		return nil, 0, err
	}
	return s.repo.ListExternalHosts(ctx, f)
}

// adminDomain loads an external domain for an administrator.
func (s *Service) adminDomain(ctx context.Context, a Actor, hostname string) (DomainView, error) {
	if err := requireAdmin(a); err != nil {
		return DomainView{}, err
	}
	name, err := lookupName(hostname)
	if err != nil {
		return DomainView{}, err
	}
	return s.repo.GetExternalHost(ctx, name)
}

// ApproveDomain lets a domain serve its site and get a certificate (admin only).
func (s *Service) ApproveDomain(ctx context.Context, a Actor, hostname string) (DomainView, error) {
	return s.review(ctx, a, hostname, true, "")
}

// RejectDomain refuses a pending domain or withdraws an approval (admin only).
func (s *Service) RejectDomain(ctx context.Context, a Actor, hostname, reason string) (DomainView, error) {
	if err := requireAdmin(a); err != nil {
		return DomainView{}, err
	}
	reason, err := domain.ValidateRejectReason(reason)
	if err != nil {
		return DomainView{}, err
	}
	return s.review(ctx, a, hostname, false, reason)
}

func (s *Service) review(ctx context.Context, a Actor, hostname string, approve bool, reason string) (DomainView, error) {
	d, err := s.adminDomain(ctx, a, hostname)
	if err != nil {
		return DomainView{}, err
	}
	unlock := s.siteLocks.lock(d.Hostname.SiteID)
	defer unlock()
	// The store re-checks that the site is active and still owns the name,
	// so a deletion that won the lock cannot be undone here.
	h, err := s.repo.ReviewExternalHost(ctx, store.ExternalReview{
		Hostname: d.Hostname.Hostname, SiteID: d.Hostname.SiteID, Approve: approve,
		Reason: reason, ReviewerID: a.User.ID, At: s.now(),
	})
	if err != nil {
		return DomainView{}, err
	}
	d.Hostname = h
	meta := map[string]any{"domain": h.Hostname, "owner": d.OwnerEmail}
	if approve {
		if err := s.files.LinkHost(h.Hostname, h.SiteID); err != nil {
			return DomainView{}, fmt.Errorf("link domain: %w", err)
		}
		s.audit(ctx, a, "domain.approve", siteTarget(h.SiteID), meta)
		return d, nil
	}
	if err := s.files.UnlinkHost(h.Hostname); err != nil {
		return DomainView{}, fmt.Errorf("unlink domain: %w", err)
	}
	meta["reason"] = reason
	s.audit(ctx, a, "domain.reject", siteTarget(h.SiteID), meta)
	return d, nil
}

// CheckDomainDNS resolves an existing external domain and compares it with
// the server's addresses (admin only). It never fails because of DNS.
func (s *Service) CheckDomainDNS(ctx context.Context, a Actor, hostname string) (dnscheck.Result, error) {
	d, err := s.adminDomain(ctx, a, hostname)
	if err != nil {
		return dnscheck.Result{}, err
	}
	return s.dns.Check(ctx, d.Hostname.Hostname, s.serverIPs(ctx)), nil
}

// DomainSetup returns the DNS targets an owner points a domain at.
func (s *Service) DomainSetup(ctx context.Context, a Actor, ref string) (DomainSetup, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return DomainSetup{}, err
	}
	return DomainSetup{ServerIPs: s.serverIPs(ctx), CNAMETarget: s.host(site.ID), MaxDomains: s.cfg.MaxDomainsPerSite}, nil
}

// serverIPs returns SERVER_IPS, or the cached addresses of the base domain
// and API host; nil when they cannot be resolved.
func (s *Service) serverIPs(ctx context.Context) []netip.Addr {
	if len(s.cfg.ServerIPs) > 0 {
		return s.cfg.ServerIPs
	}
	c := &s.ipCache
	c.mu.Lock()
	defer c.mu.Unlock()
	now := s.now()
	if now.Before(c.until) {
		return c.addrs
	}
	ctx, cancel := context.WithTimeout(ctx, serverIPsTimeout)
	defer cancel()
	addrs, err := s.dns.Addresses(ctx, s.cfg.BaseDomain, s.cfg.APIHost)
	if err != nil {
		s.log.Warn("server addresses for external domains are unknown; set SERVER_IPS", "err", err)
		return nil
	}
	c.addrs, c.until = addrs, now.Add(serverIPsTTL)
	return addrs
}
