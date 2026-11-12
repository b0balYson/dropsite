package service

import (
	"context"
	"errors"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// Release previews exist only in wildcard TLS mode: there every name under
// the base domain is covered by one certificate, so a per-release address
// costs nothing. In ondemand mode each would burn a Let's Encrypt issuance.
// Previews are derived from ready deploys and never stored in the database.

func (s *Service) previewHost(siteID, deployID string) string {
	return s.host(domain.PreviewLabel(siteID, deployID))
}

// PreviewURL returns the preview address of a release, or "" when the
// installation is not in wildcard mode or the release is not ready.
func (s *Service) PreviewURL(d domain.Deploy) string {
	if !s.cfg.Wildcard() || d.Status != domain.DeployReady {
		return ""
	}
	return s.SiteURL(s.previewHost(d.SiteID, d.ID))
}

// previewShadowed reports whether a slug taken before preview labels were
// reserved owns the preview name; such a slug wins and is never touched.
func (s *Service) previewShadowed(ctx context.Context, siteID, deployID string) (bool, error) {
	_, err := s.repo.FindSiteBySlug(ctx, domain.PreviewLabel(siteID, deployID))
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// linkPreview creates the preview of a fresh release. Failures are logged,
// not returned: the release is already live and reconciliation on the next
// start repairs the entry.
func (s *Service) linkPreview(ctx context.Context, siteID, deployID string) {
	if !s.cfg.Wildcard() {
		return
	}
	if shadowed, err := s.previewShadowed(ctx, siteID, deployID); err != nil || shadowed {
		if err != nil {
			s.log.Error("preview: check name", "site", siteID, "deploy", deployID, "err", err)
		}
		return
	}
	if err := s.files.LinkPreview(s.previewHost(siteID, deployID), siteID, deployID); err != nil {
		s.log.Error("preview: link", "site", siteID, "deploy", deployID, "err", err)
	}
}

// unlinkPreview removes a release preview in any TLS mode (a leftover from
// wildcard mode is harmless to remove). Errors are returned to the caller.
func (s *Service) unlinkPreview(ctx context.Context, siteID, deployID string) error {
	shadowed, err := s.previewShadowed(ctx, siteID, deployID)
	if err != nil || shadowed {
		return err
	}
	return s.files.UnlinkHost(s.previewHost(siteID, deployID))
}

// unlinkPreviews removes the previews of the site's ready releases among
// deploys (listed by the caller while the site was still active).
func (s *Service) unlinkPreviews(ctx context.Context, siteID string, deploys []domain.Deploy) error {
	for _, d := range deploys {
		if d.Status != domain.DeployReady {
			continue
		}
		if err := s.unlinkPreview(ctx, siteID, d.ID); err != nil {
			return err
		}
	}
	return nil
}

// toStorageLayout converts the database layout into the desired disk state,
// adding a preview for every ready release in wildcard mode. A preview whose
// name is held by a hostname row (a legacy slug) is left out.
func (s *Service) toStorageLayout(in []store.SiteLayout) []storage.SiteLayout {
	taken := map[string]bool{}
	for _, l := range in {
		for _, h := range l.Hostnames {
			taken[h] = true
		}
	}
	out := make([]storage.SiteLayout, 0, len(in))
	for _, l := range in {
		sl := storage.SiteLayout{
			SiteID: l.SiteID, CurrentDeployID: l.CurrentDeployID,
			Hostnames: l.Hostnames, ReadyDeployIDs: l.ReadyDeployIDs,
		}
		if s.cfg.Wildcard() {
			for _, d := range l.ReadyDeployIDs {
				if h := s.previewHost(l.SiteID, d); !taken[h] {
					sl.Previews = append(sl.Previews, storage.Preview{Hostname: h, DeployID: d})
				}
			}
		}
		out = append(out, sl)
	}
	return out
}
