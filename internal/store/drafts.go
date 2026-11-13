package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// DraftRepository stores the draft release of each site (change
// staged-publish).
type DraftRepository interface {
	// GetSiteDraft returns the draft row of an active site; ErrNotFound when
	// the site never had a draft or is deleted.
	GetSiteDraft(ctx context.Context, siteID string) (domain.SiteDraft, error)
	// SetSiteDraft makes deployID the site's draft and returns the previous
	// draft release, if any. A deleted or missing site → ErrNotFound.
	SetSiteDraft(ctx context.Context, siteID, deployID string, by int64, now time.Time) (*string, error)
	// ClearSiteDraft forgets the site's draft; the row (and with it the
	// preview address) stays. Clearing a site without a draft is a no-op.
	ClearSiteDraft(ctx context.Context, siteID string, now time.Time) error
	// ListDraftHosts returns the active sites whose preview address is
	// allocated, with their draft release (nil: none now).
	ListDraftHosts(ctx context.Context) (map[string]*string, error)
}

var _ DraftRepository = (*DB)(nil)

// GetSiteDraft implements DraftRepository.
func (db *DB) GetSiteDraft(ctx context.Context, siteID string) (domain.SiteDraft, error) {
	var d domain.SiteDraft
	var deploy sql.NullString
	var by sql.NullInt64
	var created, updated int64
	err := db.r.QueryRowContext(ctx, `SELECT d.site_id, d.deploy_id, d.created_by, d.created_at, d.updated_at
		FROM site_drafts d JOIN sites s ON s.id = d.site_id AND s.deleted_at IS NULL WHERE d.site_id = ?`, siteID).
		Scan(&d.SiteID, &deploy, &by, &created, &updated)
	if err != nil {
		return domain.SiteDraft{}, mapNoRows(err, "get site draft")
	}
	d.DeployID = fromNullString(deploy)
	if by.Valid {
		v := by.Int64
		d.CreatedBy = &v
	}
	d.CreatedAt, d.UpdatedAt = fromMS(created), fromMS(updated)
	return d, nil
}

// SetSiteDraft implements DraftRepository.
func (db *DB) SetSiteDraft(ctx context.Context, siteID, deployID string, by int64, now time.Time) (*string, error) {
	var prev *string
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, siteID); err != nil {
			return err
		}
		var old sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT deploy_id FROM site_drafts WHERE site_id = ?", siteID).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read site draft: %w", err)
		}
		prev = fromNullString(old)
		var creator any
		if by != 0 {
			creator = by
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_drafts (site_id, deploy_id, created_by, created_at, updated_at)
			VALUES (?1, ?2, ?3, ?4, ?4)
			ON CONFLICT (site_id) DO UPDATE SET deploy_id = excluded.deploy_id, created_by = excluded.created_by,
				created_at = excluded.created_at, updated_at = excluded.updated_at`,
			siteID, deployID, creator, toMS(now)); err != nil {
			return fmt.Errorf("set site draft: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return prev, nil
}

// ClearSiteDraft implements DraftRepository.
func (db *DB) ClearSiteDraft(ctx context.Context, siteID string, now time.Time) error {
	if _, err := db.w.ExecContext(ctx, `UPDATE site_drafts SET deploy_id = NULL, updated_at = ?
		WHERE site_id = ? AND deploy_id IS NOT NULL`, toMS(now), siteID); err != nil {
		return fmt.Errorf("clear site draft: %w", err)
	}
	return nil
}

// ListDraftHosts implements DraftRepository.
func (db *DB) ListDraftHosts(ctx context.Context) (map[string]*string, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT d.site_id, d.deploy_id FROM site_drafts d
		JOIN sites s ON s.id = d.site_id AND s.deleted_at IS NULL ORDER BY d.site_id`)
	if err != nil {
		return nil, fmt.Errorf("list draft hosts: %w", err)
	}
	defer rows.Close()
	out := map[string]*string{}
	for rows.Next() {
		var id string
		var deploy sql.NullString
		if err := rows.Scan(&id, &deploy); err != nil {
			return nil, fmt.Errorf("list draft hosts: scan: %w", err)
		}
		out[id] = fromNullString(deploy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list draft hosts: %w", err)
	}
	return out, nil
}
