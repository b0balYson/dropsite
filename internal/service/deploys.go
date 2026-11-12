package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/tracking"
)

// DeployResult is what a successful publication returns.
type DeployResult struct {
	Deploy domain.Deploy
	Site   SiteView
}

func userKey(id int64) string { return fmt.Sprintf("user:%d", id) }

// uploadLimit bounds the compressed upload: an archive of stored files can be
// slightly larger than its content because of per-file headers.
func uploadLimit(l domain.Limits) int64 {
	return l.MaxSiteBytes + int64(l.MaxFiles)*uploadPerFileBytes + uploadOverheadBytes
}

// Deploy publishes an archive read from r as a new release of the site.
// On any failure the current release stays untouched.
func (s *Service) Deploy(ctx context.Context, a Actor, ref string, r io.Reader, source domain.DeploySource) (DeployResult, error) {
	if !source.Valid() {
		source = domain.SourceAPI
	}
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return DeployResult{}, err
	}
	if ok, retry := s.deployLimit.Allow(userKey(a.User.ID)); !ok {
		return DeployResult{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": DeploysPerHour})
	}
	lim, err := s.siteOwnerLimits(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	upload, err := s.receive(r, uploadLimit(lim))
	if err != nil {
		return DeployResult{}, err
	}
	defer func() {
		if rmErr := os.Remove(upload); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			s.log.Warn("remove upload failed", "path", upload, "err", rmErr)
		}
	}()

	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	d, err := s.publish(ctx, a, site, upload, lim, source)
	if err != nil {
		return DeployResult{}, err
	}
	s.prune(ctx, site.ID, lim.KeepReleases)
	s.audit(ctx, a, "deploy.create", siteTarget(site.ID), map[string]any{
		"deploy": d.ID, "source": source, "files": d.FileCount, "bytes": d.SizeBytes,
	})
	site, err = s.repo.GetSite(ctx, site.ID)
	if err != nil {
		return DeployResult{}, err
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	return DeployResult{Deploy: d, Site: v}, nil
}

// receive streams the upload into a temporary file, aborting past max bytes.
func (s *Service) receive(r io.Reader, max int64) (string, error) {
	f, err := os.CreateTemp(s.uploadDir, "upload-*")
	if err != nil {
		return "", fmt.Errorf("create upload file: %w", err)
	}
	name := f.Name()
	n, copyErr := io.Copy(f, io.LimitReader(r, max+1))
	closeErr := f.Close()
	if copyErr == nil && n > max {
		copyErr = domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "archive is too large").
			WithParams(map[string]any{"limit": max})
	}
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(name)
		var de *domain.Error
		if errors.As(err, &de) {
			return "", de
		}
		return "", domain.E(domain.KindInvalid, domain.CodeArchiveCorrupt, "upload was interrupted").WithCause(err)
	}
	return name, nil
}

func (s *Service) publish(ctx context.Context, a Actor, site domain.Site, upload string, lim domain.Limits, source domain.DeploySource) (domain.Deploy, error) {
	now := s.now()
	id := domain.NewDeployID(now)
	staging, err := s.files.StagingDir(site.ID, id)
	if err != nil {
		return domain.Deploy{}, fmt.Errorf("prepare staging: %w", err)
	}
	res, err := s.extract(ctx, upload, staging,
		archive.Limits{MaxTotalBytes: lim.MaxSiteBytes, MaxFileBytes: lim.MaxFileBytes, MaxFiles: lim.MaxFiles}, now)
	if err != nil {
		s.discard(site.ID, id)
		return domain.Deploy{}, err
	}
	set, err := s.countersFor(ctx, site.ID)
	if err != nil {
		s.discard(site.ID, id)
		return domain.Deploy{}, err
	}
	var delta int64
	if !set.Empty() {
		inj, err := tracking.Inject(staging, set, now)
		if err != nil {
			s.discard(site.ID, id)
			return domain.Deploy{}, err
		}
		delta = inj.DeltaBytes
	}
	return s.commit(ctx, site.ID, domain.Deploy{
		ID: id, SiteID: site.ID, CreatedBy: a.User.ID, Source: source,
		SizeBytes: res.Bytes + delta, FileCount: res.Files, Status: domain.DeployReady, CreatedAt: now,
	})
}

// commit turns a filled staging directory into the site's current release.
func (s *Service) commit(ctx context.Context, siteID string, d domain.Deploy) (domain.Deploy, error) {
	if err := s.files.CommitRelease(siteID, d.ID); err != nil {
		s.discard(siteID, d.ID)
		return domain.Deploy{}, fmt.Errorf("commit release: %w", err)
	}
	if err := s.repo.InsertDeployAndActivate(ctx, d, d.CreatedAt); err != nil {
		if rmErr := s.files.DeleteRelease(siteID, d.ID); rmErr != nil {
			s.log.Error("remove orphan release failed", "site", siteID, "deploy", d.ID, "err", rmErr)
		}
		return domain.Deploy{}, err
	}
	if err := s.files.ActivateRelease(siteID, d.ID); err != nil {
		// The database already points at the release; reconciliation on the
		// next start repairs the link.
		return domain.Deploy{}, fmt.Errorf("activate release: %w", err)
	}
	s.linkPreview(ctx, siteID, d.ID)
	return d, nil
}

func (s *Service) discard(siteID, deployID string) {
	if err := s.files.DiscardStaging(siteID, deployID); err != nil {
		s.log.Error("discard staging failed", "site", siteID, "deploy", deployID, "err", err)
	}
}

// prune deletes ready releases beyond keep, newest first, never the current one.
func (s *Service) prune(ctx context.Context, siteID string, keep int) {
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		s.log.Error("prune: load site", "site", siteID, "err", err)
		return
	}
	deploys, err := s.repo.ListDeploys(ctx, siteID)
	if err != nil {
		s.log.Error("prune: list deploys", "site", siteID, "err", err)
		return
	}
	kept := 0
	var drop []string
	for _, d := range deploys {
		if d.Status != domain.DeployReady {
			continue
		}
		if site.CurrentDeployID != nil && d.ID == *site.CurrentDeployID {
			kept++
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		drop = append(drop, d.ID)
	}
	if len(drop) == 0 {
		return
	}
	if err := s.repo.MarkDeploysPruned(ctx, siteID, drop); err != nil {
		s.log.Error("prune: mark", "site", siteID, "err", err)
		return
	}
	for _, id := range drop {
		// The preview goes first so it never points at a deleted directory.
		if err := s.unlinkPreview(ctx, siteID, id); err != nil {
			s.log.Error("prune: unlink preview", "site", siteID, "deploy", id, "err", err)
		}
		if err := s.files.DeleteRelease(siteID, id); err != nil {
			s.log.Error("prune: delete files", "site", siteID, "deploy", id, "err", err)
		}
	}
}

// ListDeploys returns a site's release history, newest first.
func (s *Service) ListDeploys(ctx context.Context, a Actor, ref string) ([]domain.Deploy, SiteView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, SiteView{}, err
	}
	ds, err := s.repo.ListDeploys(ctx, site.ID)
	if err != nil {
		return nil, SiteView{}, err
	}
	v, err := s.view(ctx, site)
	return ds, v, err
}

// Rollback switches the site to a ready release; an empty deployID means the
// release just before the current one. No new release is created.
func (s *Service) Rollback(ctx context.Context, a Actor, ref, deployID string) (DeployResult, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return DeployResult{}, err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return DeployResult{}, err
	}
	target, err := s.rollbackTarget(ctx, site, deployID)
	if err != nil {
		return DeployResult{}, err
	}
	if err := s.repo.SetCurrentDeploy(ctx, site.ID, target.ID, s.now()); err != nil {
		return DeployResult{}, err
	}
	if err := s.files.ActivateRelease(site.ID, target.ID); err != nil {
		return DeployResult{}, fmt.Errorf("activate release: %w", err)
	}
	from := ""
	if site.CurrentDeployID != nil {
		from = *site.CurrentDeployID
	}
	s.audit(ctx, a, "deploy.rollback", siteTarget(site.ID), map[string]any{"from": from, "to": target.ID})
	site.CurrentDeployID = &target.ID
	v, err := s.view(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	return DeployResult{Deploy: target, Site: v}, nil
}

func (s *Service) rollbackTarget(ctx context.Context, site domain.Site, deployID string) (domain.Deploy, error) {
	notReady := domain.E(domain.KindInvalid, domain.CodeDeployNotReady, "this release is no longer available")
	if deployID != "" {
		d, err := s.repo.GetDeploy(ctx, site.ID, deployID)
		if err != nil {
			return domain.Deploy{}, err
		}
		if d.Status != domain.DeployReady {
			return domain.Deploy{}, notReady
		}
		return d, nil
	}
	deploys, err := s.repo.ListDeploys(ctx, site.ID)
	if err != nil {
		return domain.Deploy{}, err
	}
	passedCurrent := site.CurrentDeployID == nil
	for _, d := range deploys {
		if !passedCurrent {
			passedCurrent = d.ID == *site.CurrentDeployID
			continue
		}
		if d.Status == domain.DeployReady {
			return d, nil
		}
	}
	return domain.Deploy{}, domain.E(domain.KindInvalid, domain.CodeNoPrevious, "there is no earlier release to roll back to")
}

// CleanUploads removes leftover temporary uploads (crash recovery).
func (s *Service) CleanUploads() error {
	matches, err := filepath.Glob(filepath.Join(s.uploadDir, "upload-*"))
	if err != nil {
		return fmt.Errorf("list uploads: %w", err)
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove upload %s: %w", m, err)
		}
	}
	return nil
}

// UploadLimit is the largest request body a deploy to the site may send, so
// adapters can reject oversized uploads before reading them. It follows the
// limits of the site's owner; an invisible site is "not found".
func (s *Service) UploadLimit(ctx context.Context, a Actor, ref string) (int64, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return 0, err
	}
	lim, err := s.siteOwnerLimits(ctx, site)
	if err != nil {
		return 0, err
	}
	return uploadLimit(lim), nil
}
