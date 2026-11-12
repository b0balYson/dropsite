package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ScheduleRepository stores prepared releases and scheduled publishes.
type ScheduleRepository interface {
	// InsertDeploy inserts a ready release without switching the site to it.
	// A deleted or missing site → ErrNotFound.
	InsertDeploy(ctx context.Context, d domain.Deploy) error
	// CreateSchedule inserts a pending publish unless the site already has
	// maxPending of them (CodeScheduleLimit) or the release already waits
	// for one (CodeScheduleInvalid).
	CreateSchedule(ctx context.Context, sp domain.ScheduledPublish, maxPending int) (domain.ScheduledPublish, error)
	GetSchedule(ctx context.Context, id int64) (domain.ScheduledPublish, error)
	// ListSchedules returns the publishes of the given sites (every site
	// visible to ownerID, personal or of a workspace, when siteID is empty) that are pending or finished at or after
	// since, ordered by publish time.
	ListSchedules(ctx context.Context, ownerID int64, siteID string, since time.Time, limit int) ([]domain.ScheduledPublish, error)
	// ListDueSchedules returns pending publishes due at or before now, of
	// active and deleted sites alike, oldest first.
	ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]domain.ScheduledPublish, error)
	// FinishSchedule moves a pending publish to status; ErrNotFound when it
	// is no longer pending.
	FinishSchedule(ctx context.Context, id int64, status domain.ScheduleStatus, msg string, now time.Time) error
	// PendingScheduledDeploys returns the releases of a site that wait for a
	// publish.
	PendingScheduledDeploys(ctx context.Context, siteID string) (map[string]bool, error)
}

var _ ScheduleRepository = (*DB)(nil)

const scheduleCols = "id, site_id, deploy_id, created_by, publish_at, status, error, created_at, finished_at"

const scheduleColsP = "p.id, p.site_id, p.deploy_id, p.created_by, p.publish_at, p.status, p.error, p.created_at, p.finished_at"

var (
	errScheduleTaken = domain.E(domain.KindConflict, domain.CodeScheduleInvalid,
		"this release is already scheduled; cancel that publish first")
	errScheduleLimit = domain.E(domain.KindLimit, domain.CodeScheduleLimit,
		"too many scheduled publishes for this site; cancel one first")
)

func scanSchedule(s rowScanner) (domain.ScheduledPublish, error) {
	var sp domain.ScheduledPublish
	var createdBy, finished sql.NullInt64
	var at, created int64
	var status string
	if err := s.Scan(&sp.ID, &sp.SiteID, &sp.DeployID, &createdBy, &at, &status, &sp.Error, &created, &finished); err != nil {
		return domain.ScheduledPublish{}, err
	}
	sp.Status = domain.ScheduleStatus(status)
	sp.PublishAt, sp.CreatedAt = fromMS(at), fromMS(created)
	if createdBy.Valid {
		v := createdBy.Int64
		sp.CreatedBy = &v
	}
	if finished.Valid {
		t := fromMS(finished.Int64)
		sp.FinishedAt = &t
	}
	return sp, nil
}

func collectSchedules(rows *sql.Rows, op string) ([]domain.ScheduledPublish, error) {
	defer rows.Close()
	out := []domain.ScheduledPublish{}
	for rows.Next() {
		sp, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: scan: %w", op, err)
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// InsertDeploy inserts d without touching the site's current release.
func (db *DB) InsertDeploy(ctx context.Context, d domain.Deploy) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, d.SiteID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deploys
			(id, site_id, created_by, source, size_bytes, file_count, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.SiteID, d.CreatedBy, string(d.Source), d.SizeBytes, d.FileCount, string(d.Status), toMS(d.CreatedAt)); err != nil {
			return fmt.Errorf("insert deploy: %w", err)
		}
		return nil
	})
}

// CreateSchedule inserts a pending publish.
func (db *DB) CreateSchedule(ctx context.Context, sp domain.ScheduledPublish, maxPending int) (domain.ScheduledPublish, error) {
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, sp.SiteID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduled_publishes WHERE site_id = ? AND status = 'pending'",
			sp.SiteID).Scan(&n); err != nil {
			return fmt.Errorf("count schedules: %w", err)
		}
		if n >= maxPending {
			return errScheduleLimit.WithParams(map[string]any{"limit": maxPending})
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO scheduled_publishes
			(site_id, deploy_id, created_by, publish_at, status, error, created_at) VALUES (?, ?, ?, ?, 'pending', '', ?)`,
			sp.SiteID, sp.DeployID, sp.CreatedBy, toMS(sp.PublishAt), toMS(sp.CreatedAt))
		if err != nil {
			return mapUnique(err, errScheduleTaken, "insert schedule")
		}
		if sp.ID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("insert schedule: id: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.ScheduledPublish{}, err
	}
	sp.Status, sp.Error, sp.FinishedAt = domain.SchedulePending, "", nil
	sp.PublishAt, sp.CreatedAt = fromMS(toMS(sp.PublishAt)), fromMS(toMS(sp.CreatedAt))
	return sp, nil
}

// GetSchedule loads one publish of any site.
func (db *DB) GetSchedule(ctx context.Context, id int64) (domain.ScheduledPublish, error) {
	sp, err := scanSchedule(db.r.QueryRowContext(ctx, "SELECT "+scheduleCols+" FROM scheduled_publishes WHERE id = ?", id))
	if err != nil {
		return domain.ScheduledPublish{}, mapNoRows(err, "get schedule")
	}
	return sp, nil
}

// ListSchedules lists the publishes of one site or of every active site of
// an owner.
func (db *DB) ListSchedules(ctx context.Context, ownerID int64, siteID string, since time.Time, limit int) ([]domain.ScheduledPublish, error) {
	q := "SELECT " + scheduleColsP + ` FROM scheduled_publishes p
		JOIN sites s ON s.id = p.site_id AND s.deleted_at IS NULL
		WHERE (p.status = 'pending' OR p.finished_at >= ?1)`
	args := []any{toMS(since)}
	if siteID != "" {
		q += " AND p.site_id = ?2"
		args = append(args, siteID)
	} else {
		q += " AND " + visibleSite("?2")
		args = append(args, ownerID)
	}
	q += " ORDER BY p.publish_at, p.id LIMIT ?3"
	args = append(args, limit)
	rows, err := db.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return collectSchedules(rows, "list schedules")
}

// ListDueSchedules lists pending publishes due by now.
func (db *DB) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]domain.ScheduledPublish, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+scheduleCols+` FROM scheduled_publishes
		WHERE status = 'pending' AND publish_at <= ? ORDER BY publish_at, id LIMIT ?`, toMS(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	return collectSchedules(rows, "list due schedules")
}

// FinishSchedule closes a pending publish.
func (db *DB) FinishSchedule(ctx context.Context, id int64, status domain.ScheduleStatus, msg string, now time.Time) error {
	res, err := db.w.ExecContext(ctx, `UPDATE scheduled_publishes SET status = ?, error = ?, finished_at = ?
		WHERE id = ? AND status = 'pending'`, string(status), msg, toMS(now), id)
	return expectAffected(res, err, "finish schedule")
}

// PendingScheduledDeploys returns the releases of a site waiting for a publish.
func (db *DB) PendingScheduledDeploys(ctx context.Context, siteID string) (map[string]bool, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT deploy_id FROM scheduled_publishes WHERE site_id = ? AND status = 'pending'", siteID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled deploys: %w", err)
	}
	ids, err := collectStrings(rows, "list scheduled deploys")
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
