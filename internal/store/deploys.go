package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const deployColsD = "d.id, d.site_id, d.created_by, d.source, d.size_bytes, d.file_count, d.status, d.created_at"

// deployFromActive restricts deploy queries to active sites.
const deployFromActive = " FROM deploys d JOIN sites s ON s.id = d.site_id AND s.deleted_at IS NULL"

func scanDeploy(s rowScanner) (domain.Deploy, error) {
	var d domain.Deploy
	var created int64
	if err := s.Scan(&d.ID, &d.SiteID, &d.CreatedBy, &d.Source, &d.SizeBytes, &d.FileCount, &d.Status, &created); err != nil {
		return domain.Deploy{}, err
	}
	d.CreatedAt = fromMS(created)
	return d, nil
}

// InsertDeployAndActivate inserts d and makes it the site's current deploy in
// one transaction. A deleted or missing site → ErrNotFound.
func (db *DB) InsertDeployAndActivate(ctx context.Context, d domain.Deploy, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, d.SiteID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deploys
			(id, site_id, created_by, source, size_bytes, file_count, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.SiteID, d.CreatedBy, string(d.Source), d.SizeBytes, d.FileCount, string(d.Status), toMS(d.CreatedAt)); err != nil {
			return fmt.Errorf("insert deploy: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sites SET current_deploy_id = ?, updated_at = ? WHERE id = ?",
			d.ID, toMS(now), d.SiteID); err != nil {
			return fmt.Errorf("activate deploy: %w", err)
		}
		return nil
	})
}

// SetCurrentDeploy points an active site at one of its own deploys; a missing
// site or a deploy of another site → ErrNotFound. Status checks are the
// service's job.
func (db *DB) SetCurrentDeploy(ctx context.Context, siteID, deployID string, now time.Time) error {
	res, err := db.w.ExecContext(ctx, `UPDATE sites SET current_deploy_id = ?1, updated_at = ?2
		WHERE id = ?3 AND deleted_at IS NULL
		AND EXISTS (SELECT 1 FROM deploys WHERE id = ?1 AND site_id = ?3)`, deployID, toMS(now), siteID)
	return expectAffected(res, err, "set current deploy")
}

// GetDeploy loads a deploy of an active site.
func (db *DB) GetDeploy(ctx context.Context, siteID, deployID string) (domain.Deploy, error) {
	d, err := scanDeploy(db.r.QueryRowContext(ctx, "SELECT "+deployColsD+deployFromActive+
		" WHERE d.site_id = ? AND d.id = ?", siteID, deployID))
	if err != nil {
		return domain.Deploy{}, mapNoRows(err, "get deploy")
	}
	return d, nil
}

// ListDeploys returns the deploys of an active site, newest first.
func (db *DB) ListDeploys(ctx context.Context, siteID string) ([]domain.Deploy, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+deployColsD+deployFromActive+
		" WHERE d.site_id = ? ORDER BY d.created_at DESC, d.id DESC", siteID)
	if err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}
	defer rows.Close()
	out := []domain.Deploy{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deploy: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}
	return out, nil
}

// MarkDeploysPruned sets status=pruned for the listed deploys of the site.
// The current deploy is never touched, whatever the caller passes.
func (db *DB) MarkDeploysPruned(ctx context.Context, siteID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	list, err := jsonList(ids)
	if err != nil {
		return err
	}
	if _, err := db.w.ExecContext(ctx, `UPDATE deploys SET status = 'pruned'
		WHERE site_id = ?1 AND id IN (SELECT value FROM json_each(?2))
		AND id IS NOT (SELECT current_deploy_id FROM sites WHERE id = ?1)`, siteID, list); err != nil {
		return fmt.Errorf("mark deploys pruned: %w", err)
	}
	return nil
}
