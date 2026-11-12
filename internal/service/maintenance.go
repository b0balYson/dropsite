package service

import (
	"context"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// Start prepares the installation: directory layout, first administrator,
// leftover uploads and filesystem/database reconciliation (database wins).
func (s *Service) Start(ctx context.Context) error {
	if err := s.files.Init(ctx); err != nil {
		return fmt.Errorf("initialise sites volume: %w", err)
	}
	created, err := s.Bootstrap(ctx)
	if err != nil {
		return err
	}
	if created {
		s.log.Info("first administrator created", "email", s.cfg.AdminEmail)
	}
	if err := s.CleanUploads(); err != nil {
		return err
	}
	return s.Reconcile(ctx)
}

// Reconcile brings the sites volume in line with the database.
func (s *Service) Reconcile(ctx context.Context) error {
	layout, err := s.repo.ActiveLayout(ctx)
	if err != nil {
		return fmt.Errorf("load layout: %w", err)
	}
	pending, err := s.repo.ListSitesToPurge(ctx, s.now().Add(100*365*24*time.Hour))
	if err != nil {
		return fmt.Errorf("list deleted sites: %w", err)
	}
	report, err := s.files.Reconcile(ctx, toStorageLayout(layout), storage.Retained{SiteIDs: pending})
	if err != nil {
		return fmt.Errorf("reconcile sites volume: %w", err)
	}
	s.log.Info("sites volume reconciled",
		"removed_temp", len(report.RemovedTemp), "removed_releases", len(report.RemovedReleases),
		"removed_sites", len(report.RemovedSites), "linked_hosts", len(report.LinkedHosts),
		"unlinked_hosts", len(report.UnlinkedHosts), "fixed_current", report.FixedCurrent)
	if len(report.MissingCurrent) > 0 {
		s.log.Error("current release directory is missing; these sites serve nothing until redeployed",
			"sites", report.MissingCurrent)
	}
	return nil
}

func toStorageLayout(in []store.SiteLayout) []storage.SiteLayout {
	out := make([]storage.SiteLayout, 0, len(in))
	for _, l := range in {
		out = append(out, storage.SiteLayout{
			SiteID: l.SiteID, CurrentDeployID: l.CurrentDeployID,
			Hostnames: l.Hostnames, ReadyDeployIDs: l.ReadyDeployIDs,
		})
	}
	return out
}

// Housekeeping purges deleted sites past their retention and expired
// sessions and invites. It is safe to run repeatedly.
func (s *Service) Housekeeping(ctx context.Context) error {
	now := s.now()
	ids, err := s.repo.ListSitesToPurge(ctx, now.Add(-DeletedSiteTTL))
	if err != nil {
		return fmt.Errorf("list sites to purge: %w", err)
	}
	for _, id := range ids {
		if err := s.purgeSite(ctx, id, now); err != nil {
			return err
		}
	}
	if _, err := s.repo.DeleteExpiredSessions(ctx, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if _, err := s.repo.DeleteExpiredInvites(ctx, now.Add(-InviteTTL)); err != nil {
		return fmt.Errorf("delete expired invites: %w", err)
	}
	return nil
}

func (s *Service) purgeSite(ctx context.Context, id string, now time.Time) error {
	unlock := s.siteLocks.lock(id)
	defer unlock()
	if err := s.files.DeleteSite(id); err != nil {
		return fmt.Errorf("delete site files %s: %w", id, err)
	}
	if err := s.repo.MarkSitePurged(ctx, id, now); err != nil {
		return fmt.Errorf("mark site purged %s: %w", id, err)
	}
	s.audit(ctx, Actor{}, "site.purge", siteTarget(id), nil)
	return nil
}

// RunHousekeeping runs Housekeeping every interval until ctx is done.
func (s *Service) RunHousekeeping(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Housekeeping(ctx); err != nil {
			s.log.Error("housekeeping failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ListAudit returns audit entries (admin only).
func (s *Service) ListAudit(ctx context.Context, a Actor, f store.AuditFilter) ([]store.AuditRow, int, error) {
	if err := requireAdmin(a); err != nil {
		return nil, 0, err
	}
	return s.repo.ListAudit(ctx, f)
}

// ListAuditActions returns the distinct actions for the filter UI (admin only).
func (s *Service) ListAuditActions(ctx context.Context, a Actor) ([]string, error) {
	if err := requireAdmin(a); err != nil {
		return nil, err
	}
	return s.repo.ListAuditActions(ctx)
}

// Me returns the actor and effective limits.
func (s *Service) Me(ctx context.Context, a Actor) (domain.User, domain.Limits, int, error) {
	lim, err := s.Limits(ctx)
	if err != nil {
		return domain.User{}, domain.Limits{}, 0, err
	}
	n, err := s.repo.CountActiveSites(ctx, a.User.ID)
	if err != nil {
		return domain.User{}, domain.Limits{}, 0, err
	}
	return a.User, lim, n, nil
}
