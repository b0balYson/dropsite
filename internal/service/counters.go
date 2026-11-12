package service

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/tracking"
)

// SiteCounters are the analytics counters of a site: its own IDs and the
// global ones an administrator added to every site.
type SiteCounters struct {
	Own    domain.Counters
	Global domain.Counters
}

// GetSiteCounters returns a site's counters.
func (s *Service) GetSiteCounters(ctx context.Context, a Actor, ref string) (SiteCounters, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteCounters{}, err
	}
	own, err := s.repo.GetSiteCounters(ctx, site.ID)
	if err != nil {
		return SiteCounters{}, err
	}
	eff, err := s.settings(ctx)
	if err != nil {
		return SiteCounters{}, err
	}
	return SiteCounters{Own: own, Global: eff.counters}, nil
}

// countersFor is the counter set injected into a new release of the site.
func (s *Service) countersFor(ctx context.Context, siteID string) (tracking.Set, error) {
	own, err := s.repo.GetSiteCounters(ctx, siteID)
	if err != nil {
		return tracking.Set{}, err
	}
	eff, err := s.settings(ctx)
	if err != nil {
		return tracking.Set{}, err
	}
	return tracking.Merge(own, eff.counters), nil
}

// SetSiteCounters stores a site's counter IDs. Releases are immutable, so a
// site with a current release gets a new release: a copy of the current one
// with the counter block replaced. That release counts against the deploy
// rate limit. The new release is returned (nil when none was needed).
func (s *Service) SetSiteCounters(ctx context.Context, a Actor, ref string, c domain.Counters, source domain.DeploySource) (*domain.Deploy, error) {
	c, err := tracking.Normalize(c)
	if err != nil {
		return nil, err
	}
	if !source.Valid() {
		source = domain.SourceAPI
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return nil, err
	}
	prev, err := s.repo.GetSiteCounters(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	if prev == c {
		return nil, nil
	}
	if site.CurrentDeployID != nil {
		if ok, retry := s.deployLimit.Allow(userKey(a.User.ID)); !ok {
			return nil, domain.ErrRateLimited.WithRetryAfter(retry).
				WithParams(map[string]any{"limit": DeploysPerHour})
		}
	}
	if err := s.repo.SetSiteCounters(ctx, site.ID, c, s.now()); err != nil {
		return nil, err
	}
	s.audit(ctx, a, "site.counters", siteTarget(site.ID), map[string]any{
		"metrika_id": c.MetrikaID, "ga4_id": c.GA4ID,
	})
	if site.CurrentDeployID == nil {
		return nil, nil
	}
	d, err := s.rerelease(ctx, a, site, source)
	if err != nil {
		return nil, err
	}
	lim, err := s.Limits(ctx)
	if err != nil {
		return nil, err
	}
	s.prune(ctx, site.ID, lim.KeepReleases)
	s.audit(ctx, a, "deploy.create", siteTarget(site.ID), map[string]any{
		"deploy": d.ID, "source": source, "files": d.FileCount, "bytes": d.SizeBytes,
		"from": *site.CurrentDeployID, "reason": "counters",
	})
	return &d, nil
}

// rerelease copies the current release into a new one with the site's
// current counter set applied, and switches the site to it. The caller holds
// the site lock.
func (s *Service) rerelease(ctx context.Context, a Actor, site domain.Site, source domain.DeploySource) (domain.Deploy, error) {
	now := s.now()
	id := domain.NewDeployID(now)
	cp, err := s.files.CopyRelease(site.ID, *site.CurrentDeployID, id)
	if err != nil {
		return domain.Deploy{}, fmt.Errorf("copy current release: %w", err)
	}
	staging, err := s.files.StagingDir(site.ID, id)
	if err != nil {
		s.discard(site.ID, id)
		return domain.Deploy{}, fmt.Errorf("prepare staging: %w", err)
	}
	set, err := s.countersFor(ctx, site.ID)
	if err != nil {
		s.discard(site.ID, id)
		return domain.Deploy{}, err
	}
	inj, err := tracking.Inject(staging, set, now)
	if err != nil {
		s.discard(site.ID, id)
		return domain.Deploy{}, err
	}
	d, err := s.commit(ctx, site.ID, domain.Deploy{
		ID: id, SiteID: site.ID, CreatedBy: a.User.ID, Source: source,
		SizeBytes: cp.Bytes + inj.DeltaBytes, FileCount: cp.Files, Status: domain.DeployReady, CreatedAt: now,
	})
	if err == nil {
		s.copyReport(ctx, site.ID, *site.CurrentDeployID, d.ID)
	}
	return d, err
}
