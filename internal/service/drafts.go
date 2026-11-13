package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/storage"
)

// Draft releases (change staged-publish). A draft is a ready release the
// site is not switched to, shown at one stable preview address per site,
// p-<site id>.<BASE_DOMAIN>, in both TLS modes. The address is allocated
// with the first draft and stays for the site's lifetime, serving nothing
// while there is no draft, so ondemand mode issues one certificate per site.

// PublishOptions selects how a publication goes live.
type PublishOptions struct {
	// Draft builds the release as the site's draft instead of switching the
	// site to it; a previous draft is replaced.
	Draft bool
}

// publishMode is what happens to a freshly built release.
type publishMode int

const (
	modeLive     publishMode = iota // the site switches to it
	modePrepared                    // kept for a scheduled publish
	modeDraft                       // becomes the site's draft
)

func (o PublishOptions) mode() publishMode {
	if o.Draft {
		return modeDraft
	}
	return modeLive
}

// DraftView is a site's current draft.
type DraftView struct {
	Deploy    domain.Deploy
	CreatedAt time.Time
	// PreviewURL is the stable preview address; empty only when a legacy
	// slug owns the name.
	PreviewURL string
}

// publishIn builds the release from upload and applies mode. The caller
// holds the site lock.
func (s *Service) publishIn(ctx context.Context, a Actor, site domain.Site, upload string, lim domain.Limits,
	source domain.DeploySource, mode publishMode) (domain.Deploy, error) {
	switch mode {
	case modePrepared:
		return s.prepareRelease(ctx, a, site, upload, lim, source)
	case modeDraft:
		d, err := s.prepareRelease(ctx, a, site, upload, lim, source)
		if err != nil {
			return domain.Deploy{}, err
		}
		return d, s.setDraft(ctx, site.ID, d)
	default:
		return s.publish(ctx, a, site, upload, lim, source)
	}
}

func (s *Service) draftHost(siteID string) string { return s.host(domain.DraftPreviewLabel(siteID)) }

// draftShadowed reports whether a slug taken before the draft label was
// reserved owns the site's preview name; such a slug wins.
func (s *Service) draftShadowed(ctx context.Context, siteID string) (bool, error) {
	_, err := s.repo.FindSiteBySlug(ctx, domain.DraftPreviewLabel(siteID))
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// setDraft makes d the site's draft, repoints the preview and drops the
// draft it replaces. The caller holds the site lock.
func (s *Service) setDraft(ctx context.Context, siteID string, d domain.Deploy) error {
	prev, err := s.repo.SetSiteDraft(ctx, siteID, d.ID, d.CreatedBy, s.now())
	if err != nil {
		return err
	}
	s.linkDraft(ctx, siteID, &d.ID)
	if prev != nil && *prev != d.ID {
		s.dropRelease(ctx, siteID, *prev)
	}
	return nil
}

// linkDraft points the site's preview at deployID (nil: nothing), creating
// the host entry with the site's markers first. Failures are logged: the
// database is the source of truth and reconciliation repairs the disk.
func (s *Service) linkDraft(ctx context.Context, siteID string, deployID *string) {
	shadowed, err := s.draftShadowed(ctx, siteID)
	if err != nil || shadowed {
		if err != nil {
			s.log.Error("draft: check preview name", "site", siteID, "err", err)
		}
		return
	}
	host := s.draftHost(siteID)
	err = s.withHostFlags(ctx, siteID, host, func() error {
		if err := s.files.LinkDraft(siteID, deployID); err != nil {
			return err
		}
		return s.files.LinkDraftHost(host, siteID)
	})
	if err != nil {
		s.log.Error("draft: link preview", "site", siteID, "err", err)
	}
}

// dropRelease prunes one release that is no longer needed (a replaced or
// discarded draft) unless it is current or waits for a scheduled publish.
func (s *Service) dropRelease(ctx context.Context, siteID, deployID string) {
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		s.log.Error("draft: load site", "site", siteID, "err", err)
		return
	}
	if site.CurrentDeployID != nil && *site.CurrentDeployID == deployID {
		return
	}
	scheduled, err := s.repo.PendingScheduledDeploys(ctx, siteID)
	if err != nil {
		s.log.Error("draft: list scheduled", "site", siteID, "err", err)
		return
	}
	if scheduled[deployID] {
		return
	}
	if err := s.repo.MarkDeploysPruned(ctx, siteID, []string{deployID}); err != nil {
		s.log.Error("draft: mark pruned", "site", siteID, "deploy", deployID, "err", err)
		return
	}
	s.dropReports(ctx, siteID, []string{deployID})
	if err := s.unlinkPreview(ctx, siteID, deployID); err != nil {
		s.log.Error("draft: unlink release preview", "site", siteID, "deploy", deployID, "err", err)
	}
	if err := s.files.DeleteRelease(siteID, deployID); err != nil {
		s.log.Error("draft: delete files", "site", siteID, "deploy", deployID, "err", err)
	}
}

// draftDeployID returns the release recorded as the site's draft, valid or
// not, or "".
func (s *Service) draftDeployID(ctx context.Context, siteID string) (string, error) {
	row, err := s.repo.GetSiteDraft(ctx, siteID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && row.DeployID == nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return *row.DeployID, nil
}

// activeDraft returns the site's draft release, or nil when there is none:
// a draft that is no longer ready or became current (a rollback onto it)
// does not count.
func (s *Service) activeDraft(ctx context.Context, site domain.Site) (*domain.Deploy, *domain.SiteDraft, error) {
	row, err := s.repo.GetSiteDraft(ctx, site.ID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && row.DeployID == nil) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if site.CurrentDeployID != nil && *site.CurrentDeployID == *row.DeployID {
		return nil, nil, nil
	}
	d, err := s.repo.GetDeploy(ctx, site.ID, *row.DeployID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && d.Status != domain.DeployReady) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &d, &row, nil
}

// draftView describes the site's draft for adapters, nil without one.
func (s *Service) draftView(ctx context.Context, site domain.Site) (*DraftView, error) {
	d, row, err := s.activeDraft(ctx, site)
	if err != nil || d == nil {
		return nil, err
	}
	v := &DraftView{Deploy: *d, CreatedAt: row.CreatedAt}
	shadowed, err := s.draftShadowed(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	if !shadowed {
		v.PreviewURL = s.SiteURL(s.draftHost(site.ID))
	}
	return v, nil
}

// heldDeploys lists the releases of a site that pruning and a rollback to
// "the previous release" must skip: those waiting for a scheduled publish
// and the draft.
func (s *Service) heldDeploys(ctx context.Context, siteID string) (map[string]bool, error) {
	held, err := s.repo.PendingScheduledDeploys(ctx, siteID)
	if err != nil {
		return nil, err
	}
	draft, err := s.draftDeployID(ctx, siteID)
	if err != nil {
		return nil, err
	}
	if draft != "" {
		held[draft] = true
	}
	return held, nil
}

// isDraftBase reports whether base is the site's draft, so edits can build
// on it (update_site_files, patch uploads).
func (s *Service) isDraftBase(ctx context.Context, site domain.Site, base string) (bool, error) {
	d, _, err := s.activeDraft(ctx, site)
	if err != nil || d == nil {
		return false, err
	}
	return d.ID == base, nil
}

// PublishDraft switches the site to its draft, like a rollback: no new
// release is created. Without a draft it fails with no_draft.
func (s *Service) PublishDraft(ctx context.Context, a Actor, ref string) (DeployResult, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return DeployResult{}, err
	}
	lim, err := s.siteOwnerLimits(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return DeployResult{}, err
	}
	d, _, err := s.activeDraft(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	if d == nil {
		return DeployResult{}, s.forgetDraft(ctx, site.ID)
	}
	if err := s.repo.SetCurrentDeploy(ctx, site.ID, d.ID, s.now()); err != nil {
		return DeployResult{}, err
	}
	if err := s.files.ActivateRelease(site.ID, d.ID); err != nil {
		return DeployResult{}, fmt.Errorf("activate release: %w", err)
	}
	if err := s.repo.ClearSiteDraft(ctx, site.ID, s.now()); err != nil {
		return DeployResult{}, err
	}
	s.linkDraft(ctx, site.ID, nil)
	from := ""
	if site.CurrentDeployID != nil {
		from = *site.CurrentDeployID
	}
	s.audit(ctx, a, "draft.publish", siteTarget(site.ID), map[string]any{"from": from, "to": d.ID})
	s.prune(ctx, site.ID, lim.KeepReleases)
	return s.resultOf(ctx, site.ID, *d)
}

// DiscardDraft deletes the site's draft; the preview then answers 404.
// Without a draft it fails with no_draft.
func (s *Service) DiscardDraft(ctx context.Context, a Actor, ref string) (SiteView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SiteView{}, err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return SiteView{}, err
	}
	d, _, err := s.activeDraft(ctx, site)
	if err != nil {
		return SiteView{}, err
	}
	if d == nil {
		return SiteView{}, s.forgetDraft(ctx, site.ID)
	}
	if err := s.repo.ClearSiteDraft(ctx, site.ID, s.now()); err != nil {
		return SiteView{}, err
	}
	s.linkDraft(ctx, site.ID, nil)
	s.dropRelease(ctx, site.ID, d.ID)
	s.audit(ctx, a, "draft.discard", siteTarget(site.ID), map[string]any{"deploy": d.ID})
	return s.view(ctx, site)
}

// forgetDraft clears a stale draft record (its release became current or
// was pruned) and reports that there is no draft.
func (s *Service) forgetDraft(ctx context.Context, siteID string) error {
	if err := s.repo.ClearSiteDraft(ctx, siteID, s.now()); err != nil {
		return err
	}
	s.linkDraft(ctx, siteID, nil)
	return domain.ErrNoDraft
}

// resultOf reloads the site after a switch and returns the result for d.
func (s *Service) resultOf(ctx context.Context, siteID string, d domain.Deploy) (DeployResult, error) {
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		return DeployResult{}, err
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	return DeployResult{Deploy: d, Site: v, Quality: s.reportOf(ctx, siteID, d.ID)}, nil
}

// draftHostOf returns the served draft preview name of a site, or "" when
// none is allocated or a legacy slug owns it.
func (s *Service) draftHostOf(ctx context.Context, siteID string) (string, error) {
	_, err := s.repo.GetSiteDraft(ctx, siteID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	shadowed, err := s.draftShadowed(ctx, siteID)
	if err != nil || shadowed {
		return "", err
	}
	return s.draftHost(siteID), nil
}

// siteOfDraftHost resolves a draft preview hostname to its active site. ok is
// false when host is not of that form.
func (s *Service) siteOfDraftHost(ctx context.Context, host string) (site domain.Site, ok bool, err error) {
	siteID, ok := s.draftPreviewSite(host)
	if !ok {
		return domain.Site{}, false, nil
	}
	h, err := s.draftHostOf(ctx, siteID)
	if err != nil {
		return domain.Site{}, true, err
	}
	if h != host {
		return domain.Site{}, true, domain.ErrNotFound
	}
	site, err = s.repo.GetSite(ctx, siteID)
	return site, true, err
}

// draftPreviewSite maps a host of the draft preview form to its site ID.
func (s *Service) draftPreviewSite(host string) (string, bool) {
	label, ok := strings.CutSuffix(host, "."+s.cfg.BaseDomain)
	if !ok || !domain.IsDraftPreviewLabel(label) {
		return "", false
	}
	return label[2:], true
}

// unlinkDraftHost removes the site's draft preview from serving (site
// deletion); an entry owned by a legacy slug is left alone.
func (s *Service) unlinkDraftHost(ctx context.Context, siteID string) error {
	shadowed, err := s.draftShadowed(ctx, siteID)
	if err != nil || shadowed {
		return err
	}
	return s.unlinkHost(s.draftHost(siteID))
}

// withDraftLayouts adds the allocated draft previews to the desired disk
// state. A name held by a hostname (a legacy slug) is left out.
func (s *Service) withDraftLayouts(ctx context.Context, layout []storage.SiteLayout) ([]storage.SiteLayout, error) {
	drafts, err := s.repo.ListDraftHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list draft previews: %w", err)
	}
	taken := map[string]bool{}
	for _, l := range layout {
		for _, h := range l.Hostnames {
			taken[h] = true
		}
		for _, p := range l.Previews {
			taken[p.Hostname] = true
		}
	}
	for i := range layout {
		deploy, ok := drafts[layout[i].SiteID]
		host := s.draftHost(layout[i].SiteID)
		if !ok || taken[host] {
			continue
		}
		layout[i].DraftHost = host
		if deploy != nil && slices.Contains(layout[i].ReadyDeployIDs, *deploy) {
			layout[i].DraftDeployID = deploy
		}
	}
	return layout, nil
}
