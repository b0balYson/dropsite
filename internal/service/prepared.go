package service

import (
	"context"
	"fmt"

	"github.com/b0balYson/dropsite/internal/domain"
)

// prepareRelease builds a ready release from the upload without switching
// the site to it; a scheduled publish switches later. The release gets a
// preview like any other. The caller holds the site lock.
func (s *Service) prepareRelease(ctx context.Context, a Actor, site domain.Site, upload string, lim domain.Limits, source domain.DeploySource) (domain.Deploy, error) {
	d, rep, err := s.stage(ctx, a, site, upload, lim, source)
	if err != nil {
		return domain.Deploy{}, err
	}
	if err := s.files.CommitRelease(site.ID, d.ID); err != nil {
		s.discard(site.ID, d.ID)
		return domain.Deploy{}, fmt.Errorf("commit release: %w", err)
	}
	if err := s.repo.InsertDeploy(ctx, d); err != nil {
		if rmErr := s.files.DeleteRelease(site.ID, d.ID); rmErr != nil {
			s.log.Error("remove orphan release failed", "site", site.ID, "deploy", d.ID, "err", rmErr)
		}
		return domain.Deploy{}, err
	}
	s.saveReport(ctx, site.ID, d.ID, rep)
	s.linkPreview(ctx, site.ID, d.ID)
	return d, nil
}
