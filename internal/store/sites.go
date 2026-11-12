package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const (
	siteInsertCols = "id, owner_id, title, current_deploy_id, created_at, updated_at, deleted_at, purged_at"
	siteCols       = siteInsertCols + ", noindex, access_password_hash != '', expires_at"
	siteColsS      = "s.id, s.owner_id, s.title, s.current_deploy_id, s.created_at, s.updated_at, s.deleted_at, s.purged_at, " +
		"s.noindex, s.access_password_hash != '', s.expires_at"
)

// siteScan holds raw values of siteCols: dest gives the scan targets, finish
// converts them into the site.
type siteScan struct {
	current          sql.NullString
	created, updated int64
	deleted, purged  sql.NullInt64
	expires          sql.NullInt64
}

func (r *siteScan) dest(site *domain.Site) []any {
	return []any{&site.ID, &site.OwnerID, &site.Title, &r.current, &r.created, &r.updated, &r.deleted, &r.purged,
		&site.Noindex, &site.Protected, &r.expires}
}

func (r *siteScan) finish(site *domain.Site) {
	site.CurrentDeployID = fromNullString(r.current)
	site.CreatedAt, site.UpdatedAt = fromMS(r.created), fromMS(r.updated)
	site.DeletedAt, site.PurgedAt = fromNullMS(r.deleted), fromNullMS(r.purged)
	site.ExpiresAt = fromNullMS(r.expires)
}

func scanSite(s rowScanner) (domain.Site, error) {
	var site domain.Site
	var raw siteScan
	if err := s.Scan(raw.dest(&site)...); err != nil {
		return domain.Site{}, err
	}
	raw.finish(&site)
	return site, nil
}

// SiteIDExists reports whether the ID was ever used, tombstones included.
func (db *DB) SiteIDExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	if err := db.r.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sites WHERE id = ?)", id).Scan(&ok); err != nil {
		return false, fmt.Errorf("check site id: %w", err)
	}
	return ok, nil
}

// CountActiveSites counts the owner's sites that are not deleted.
func (db *DB) CountActiveSites(ctx context.Context, ownerID int64) (int, error) {
	var n int
	if err := db.r.QueryRowContext(ctx, "SELECT count(*) FROM sites WHERE owner_id = ? AND deleted_at IS NULL",
		ownerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count sites: %w", err)
	}
	return n, nil
}

// CreateSite inserts the site and its hostnames in one transaction.
func (db *DB) CreateSite(ctx context.Context, s domain.Site, hosts []domain.Hostname) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sites (`+siteInsertCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.OwnerID, s.Title, s.CurrentDeployID, toMS(s.CreatedAt), toMS(s.UpdatedAt),
			nullMS(s.DeletedAt), nullMS(s.PurgedAt)); err != nil {
			return fmt.Errorf("insert site %s: %w", s.ID, err)
		}
		for _, h := range hosts {
			h.SiteID = s.ID
			if err := insertHost(ctx, tx, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetSite loads an active site.
func (db *DB) GetSite(ctx context.Context, id string) (domain.Site, error) {
	s, err := scanSite(db.r.QueryRowContext(ctx, "SELECT "+siteCols+" FROM sites WHERE id = ? AND deleted_at IS NULL", id))
	if err != nil {
		return domain.Site{}, mapNoRows(err, "get site")
	}
	return s, nil
}

// FindSiteBySlug resolves a slug label to its active site. The range on the
// hostname primary key matches "<slug>.<anything>" without LIKE.
func (db *DB) FindSiteBySlug(ctx context.Context, slug string) (domain.Site, error) {
	s, err := scanSite(db.r.QueryRowContext(ctx, "SELECT "+siteColsS+` FROM hostnames h
		JOIN sites s ON s.id = h.site_id
		WHERE h.kind = 'slug' AND h.hostname > ? AND h.hostname < ? AND s.deleted_at IS NULL
		LIMIT 1`, slug+".", slug+"/"))
	if err != nil {
		return domain.Site{}, mapNoRows(err, "find site by slug")
	}
	return s, nil
}

// UpdateSiteTitle renames an active site.
func (db *DB) UpdateSiteTitle(ctx context.Context, id, title string, now time.Time) error {
	res, err := db.w.ExecContext(ctx, "UPDATE sites SET title = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		title, toMS(now), id)
	return expectAffected(res, err, "update site title")
}

// TransferSite changes the owner of an active site.
func (db *DB) TransferSite(ctx context.Context, siteID string, newOwnerID int64, now time.Time) error {
	res, err := db.w.ExecContext(ctx, "UPDATE sites SET owner_id = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		newOwnerID, toMS(now), siteID)
	return expectAffected(res, err, "transfer site")
}

// SoftDeleteSite marks the site deleted and frees all its hostnames at once.
func (db *DB) SoftDeleteSite(ctx context.Context, siteID string, now time.Time) ([]string, error) {
	var removed []string
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE sites SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
			toMS(now), toMS(now), siteID)
		if err := expectAffected(res, err, "delete site"); err != nil {
			return err
		}
		if removed, err = siteHostnames(ctx, tx, siteID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM hostnames WHERE site_id = ?", siteID); err != nil {
			return fmt.Errorf("delete hostnames: %w", err)
		}
		return deleteSiteCollections(ctx, tx, siteID)
	})
	return removed, err
}

// ListSitesToPurge returns deleted, not yet purged sites deleted before the given time.
func (db *DB) ListSitesToPurge(ctx context.Context, deletedBefore time.Time) ([]string, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT id FROM sites
		WHERE deleted_at IS NOT NULL AND deleted_at < ? AND purged_at IS NULL ORDER BY deleted_at, id`,
		toMS(deletedBefore))
	if err != nil {
		return nil, fmt.Errorf("list sites to purge: %w", err)
	}
	return collectStrings(rows, "list sites to purge")
}

// MarkSitePurged deletes the deploy rows of a deleted site and sets purged_at.
// The site row stays as a tombstone. An active or missing site → ErrNotFound.
func (db *DB) MarkSitePurged(ctx context.Context, siteID string, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE sites SET purged_at = ?, current_deploy_id = NULL, access_password_hash = ''
			WHERE id = ? AND deleted_at IS NOT NULL`, toMS(now), siteID)
		if err := expectAffected(res, err, "mark site purged"); err != nil {
			return err
		}
		if err := deletePublishData(ctx, tx, siteID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM deploys WHERE site_id = ?", siteID); err != nil {
			return fmt.Errorf("delete deploys: %w", err)
		}
		return deleteSiteStats(ctx, tx, siteID)
	})
}

// requireActiveSite returns ErrNotFound unless the site exists and is not deleted.
func requireActiveSite(ctx context.Context, q querier, siteID string) error {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM sites WHERE id = ? AND deleted_at IS NULL", siteID).Scan(&one)
	if err != nil {
		return mapNoRows(err, "check site")
	}
	return nil
}

// collectStrings reads a single string column and closes rows.
func collectStrings(rows *sql.Rows, op string) ([]string, error) {
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("%s: scan: %w", op, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}
