package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Scheduled publishes (change chat-workflow).
const (
	// MaxPendingSchedules caps the publishes waiting on one site.
	MaxPendingSchedules = 20
	// MaxScheduleAhead is how far ahead a publish may be scheduled.
	MaxScheduleAhead = 365 * 24 * time.Hour
	// WorkflowInterval is how often due publishes are run.
	WorkflowInterval = 15 * time.Second
	// scheduleHistory is how long finished publishes stay listed.
	scheduleHistory  = 7 * 24 * time.Hour
	maxListSchedules = 100
	dueBatch         = 100
)

// ScheduleView is a scheduled publish with the links to show.
type ScheduleView struct {
	domain.ScheduledPublish
	SiteTitle  string
	SiteURL    string
	PreviewURL string
}

func errSchedule(msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeScheduleInvalid, msg)
}

// ParsePublishAt reads an RFC 3339 time with an explicit time zone.
func ParsePublishAt(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errSchedule("publish_at must be a date and time with a time zone, " +
			"e.g. 2026-12-01T09:00:00+03:00; use the user's time zone")
	}
	return t.UTC(), nil
}

func (s *Service) checkPublishAt(t time.Time) error {
	now := s.now()
	if !t.After(now) {
		return errSchedule("publish_at must be in the future")
	}
	if t.Sub(now) > MaxScheduleAhead {
		return errSchedule("publish_at must be at most 365 days ahead")
	}
	return nil
}

// ScheduleRelease schedules a ready, not served release of a site.
func (s *Service) ScheduleRelease(ctx context.Context, a Actor, ref, deployID string, at time.Time) (ScheduleView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return ScheduleView{}, err
	}
	if err := s.checkPublishAt(at); err != nil {
		return ScheduleView{}, err
	}
	if strings.TrimSpace(deployID) == "" {
		return ScheduleView{}, errSchedule("deploy_id is required: pick a release from list_deploys")
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return ScheduleView{}, err
	}
	d, err := s.repo.GetDeploy(ctx, site.ID, deployID)
	if err != nil {
		return ScheduleView{}, err
	}
	if d.Status != domain.DeployReady {
		return ScheduleView{}, domain.E(domain.KindInvalid, domain.CodeDeployNotReady, "this release is no longer available")
	}
	if site.CurrentDeployID != nil && *site.CurrentDeployID == d.ID {
		return ScheduleView{}, errSchedule("this release is already being served")
	}
	return s.createSchedule(ctx, a, site, d.ID, at)
}

func (s *Service) createSchedule(ctx context.Context, a Actor, site domain.Site, deployID string, at time.Time) (ScheduleView, error) {
	sp, err := s.repo.CreateSchedule(ctx, domain.ScheduledPublish{
		SiteID: site.ID, DeployID: deployID, CreatedBy: a.id(), PublishAt: at, CreatedAt: s.now(),
	}, MaxPendingSchedules)
	if err != nil {
		return ScheduleView{}, err
	}
	s.audit(ctx, a, "schedule.create", siteTarget(site.ID), map[string]any{
		"schedule": sp.ID, "deploy": deployID, "publish_at": at.UTC().Format(time.RFC3339),
	})
	return s.scheduleView(ctx, sp, map[string]SiteView{})
}

// ScheduleUpload closes an upload session into a prepared release and
// schedules it. The time is checked before the session is closed.
func (s *Service) ScheduleUpload(ctx context.Context, a Actor, uploadID string, at time.Time) (ScheduleView, error) {
	if err := s.checkPublishAt(at); err != nil {
		return ScheduleView{}, err
	}
	u, err := s.session(a, uploadID, s.now())
	if err != nil {
		return ScheduleView{}, err
	}
	if u.siteID != "" {
		pending, err := s.repo.PendingScheduledDeploys(ctx, u.siteID)
		if err != nil {
			return ScheduleView{}, err
		}
		if len(pending) >= MaxPendingSchedules {
			return ScheduleView{}, domain.E(domain.KindLimit, domain.CodeScheduleLimit,
				"too many scheduled publishes for this site; cancel one first").
				WithParams(map[string]any{"limit": MaxPendingSchedules})
		}
	}
	res, err := s.finishUpload(ctx, a, uploadID, nil, modePrepared)
	if err != nil {
		return ScheduleView{}, err
	}
	site, err := s.repo.GetSite(ctx, res.Site.Site.ID)
	if err != nil {
		return ScheduleView{}, err
	}
	return s.createSchedule(ctx, a, site, res.Deploy.ID, at)
}

// ListSchedules returns the pending and recently finished publishes of one
// site, or of every own site when ref is empty.
func (s *Service) ListSchedules(ctx context.Context, a Actor, ref string) ([]ScheduleView, error) {
	siteID := ""
	if ref != "" {
		site, err := s.resolveSite(ctx, a, ref)
		if err != nil {
			return nil, err
		}
		siteID = site.ID
	}
	rows, err := s.repo.ListSchedules(ctx, a.User.ID, siteID, s.now().Add(-scheduleHistory), maxListSchedules)
	if err != nil {
		return nil, err
	}
	sites := map[string]SiteView{}
	out := make([]ScheduleView, 0, len(rows))
	for _, sp := range rows {
		v, err := s.scheduleView(ctx, sp, sites)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// CancelSchedule cancels a pending publish of a visible site.
func (s *Service) CancelSchedule(ctx context.Context, a Actor, id int64) (ScheduleView, error) {
	sp, err := s.repo.GetSchedule(ctx, id)
	if err != nil {
		return ScheduleView{}, err
	}
	site, err := s.resolveSite(ctx, a, sp.SiteID)
	if err != nil {
		return ScheduleView{}, err
	}
	notPending := domain.E(domain.KindConflict, domain.CodeScheduleNotPending,
		"this publish is not waiting any more: it was done, cancelled or failed")
	if sp.Status != domain.SchedulePending {
		return ScheduleView{}, notPending
	}
	err = s.repo.FinishSchedule(ctx, id, domain.ScheduleCancelled, "", s.now())
	if errors.Is(err, domain.ErrNotFound) {
		return ScheduleView{}, notPending
	}
	if err != nil {
		return ScheduleView{}, err
	}
	s.audit(ctx, a, "schedule.cancel", siteTarget(site.ID), map[string]any{"schedule": id, "deploy": sp.DeployID})
	if sp, err = s.repo.GetSchedule(ctx, id); err != nil {
		return ScheduleView{}, err
	}
	return s.scheduleView(ctx, sp, map[string]SiteView{})
}

func (s *Service) scheduleView(ctx context.Context, sp domain.ScheduledPublish, sites map[string]SiteView) (ScheduleView, error) {
	v, ok := sites[sp.SiteID]
	if !ok {
		site, err := s.repo.GetSite(ctx, sp.SiteID)
		if err != nil {
			return ScheduleView{}, err
		}
		if v, err = s.view(ctx, site); err != nil {
			return ScheduleView{}, err
		}
		sites[sp.SiteID] = v
	}
	out := ScheduleView{ScheduledPublish: sp, SiteTitle: v.Site.Title, SiteURL: v.URL}
	if sp.Status == domain.SchedulePending {
		out.PreviewURL = s.PreviewURL(domain.Deploy{ID: sp.DeployID, SiteID: sp.SiteID, Status: domain.DeployReady})
	}
	return out, nil
}

// RunDueSchedules performs every publish that is due. Publishes missed
// while control was down are due too.
func (s *Service) RunDueSchedules(ctx context.Context) error {
	due, err := s.repo.ListDueSchedules(ctx, s.now(), dueBatch)
	if err != nil {
		return fmt.Errorf("list due publishes: %w", err)
	}
	for _, sp := range due {
		if err := s.runSchedule(ctx, sp); err != nil {
			return fmt.Errorf("scheduled publish %d: %w", sp.ID, err)
		}
	}
	return nil
}

// runSchedule switches the site under its lock, like a rollback, after
// re-checking the publish, the site and the release.
func (s *Service) runSchedule(ctx context.Context, sp domain.ScheduledPublish) error {
	unlock := s.siteLocks.lock(sp.SiteID)
	defer unlock()
	sp, err := s.repo.GetSchedule(ctx, sp.ID)
	if err != nil || sp.Status != domain.SchedulePending {
		return ignoreNotFound(err)
	}
	site, err := s.repo.GetSite(ctx, sp.SiteID)
	if errors.Is(err, domain.ErrNotFound) {
		return s.finishSchedule(ctx, sp, domain.ScheduleCancelled, "the site was deleted", "")
	}
	if err != nil {
		return err
	}
	d, err := s.repo.GetDeploy(ctx, site.ID, sp.DeployID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && d.Status != domain.DeployReady) {
		return s.finishSchedule(ctx, sp, domain.ScheduleFailed, "the release is no longer available", "")
	}
	if err != nil {
		return err
	}
	from := ""
	if site.CurrentDeployID != nil {
		from = *site.CurrentDeployID
	}
	if from != d.ID {
		if err := s.repo.SetCurrentDeploy(ctx, site.ID, d.ID, s.now()); err != nil {
			return err
		}
		if err := s.files.ActivateRelease(site.ID, d.ID); err != nil {
			// The database already serves the release; reconciliation on
			// the next start repairs the link.
			s.log.Error("scheduled publish: activate release", "site", site.ID, "deploy", d.ID, "err", err)
		}
	}
	s.dropDraftIfLive(ctx, site.ID, d.ID)
	return s.finishSchedule(ctx, sp, domain.ScheduleDone, "", from)
}

func (s *Service) finishSchedule(ctx context.Context, sp domain.ScheduledPublish, st domain.ScheduleStatus, msg, from string) error {
	if err := s.repo.FinishSchedule(ctx, sp.ID, st, msg, s.now()); err != nil {
		return ignoreNotFound(err)
	}
	action := map[domain.ScheduleStatus]string{
		domain.ScheduleDone: "schedule.publish", domain.ScheduleFailed: "schedule.fail", domain.ScheduleCancelled: "schedule.cancel",
	}[st]
	meta := map[string]any{"schedule": sp.ID, "deploy": sp.DeployID}
	if sp.CreatedBy != nil {
		meta["created_by"] = *sp.CreatedBy
	}
	if msg != "" {
		meta["reason"] = msg
	}
	if st == domain.ScheduleDone {
		meta["from"] = from
	}
	s.audit(ctx, Actor{}, action, siteTarget(sp.SiteID), meta)
	return nil
}

func ignoreNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

// RunWorkflow runs due publishes now and then every interval, and prunes
// the screenshot cache hourly, until ctx is done.
func (s *Service) RunWorkflow(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastPurge time.Time
	for {
		if err := s.RunDueSchedules(ctx); err != nil {
			s.log.Error("scheduled publishes failed", "err", err)
		}
		if now := s.now(); now.Sub(lastPurge) >= time.Hour {
			s.purgeScreenshots(now)
			lastPurge = now
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
