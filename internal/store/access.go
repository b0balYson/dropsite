package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// FindSiteByHostname resolves an active hostname to its active site.
func (db *DB) FindSiteByHostname(ctx context.Context, hostname string) (domain.Site, error) {
	s, err := scanSite(db.r.QueryRowContext(ctx, "SELECT "+siteColsS+` FROM hostnames h
		JOIN sites s ON s.id = h.site_id
		WHERE h.hostname = ? AND s.deleted_at IS NULL`, hostname))
	if err != nil {
		return domain.Site{}, mapNoRows(err, "find site by hostname")
	}
	return s, nil
}

// SetSiteNoindex sets the noindex flag of an active site.
func (db *DB) SetSiteNoindex(ctx context.Context, siteID string, on bool, now time.Time) error {
	res, err := db.w.ExecContext(ctx, "UPDATE sites SET noindex = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		on, toMS(now), siteID)
	return expectAffected(res, err, "set noindex")
}

// GetSiteAccess returns the visitor password state of an active site.
func (db *DB) GetSiteAccess(ctx context.Context, siteID string) (domain.SiteAccess, error) {
	var a domain.SiteAccess
	err := db.r.QueryRowContext(ctx, `SELECT access_password_hash, access_version FROM sites
		WHERE id = ? AND deleted_at IS NULL`, siteID).Scan(&a.PasswordHash, &a.Version)
	if err != nil {
		return domain.SiteAccess{}, mapNoRows(err, "get site access")
	}
	return a, nil
}

// SetSitePassword stores the visitor password hash ("" opens the site) and
// increments access_version, invalidating every visitor cookie.
func (db *DB) SetSitePassword(ctx context.Context, siteID, hash string, now time.Time) (int64, error) {
	var version int64
	err := db.w.QueryRowContext(ctx, `UPDATE sites SET access_password_hash = ?, access_version = access_version + 1,
		updated_at = ? WHERE id = ? AND deleted_at IS NULL RETURNING access_version`,
		hash, toMS(now), siteID).Scan(&version)
	if err != nil {
		return 0, mapNoRows(err, "set site password")
	}
	return version, nil
}

// SetSiteExpiry sets or clears the expiry of an active site.
func (db *DB) SetSiteExpiry(ctx context.Context, siteID string, expiresAt *time.Time, now time.Time) error {
	res, err := db.w.ExecContext(ctx, "UPDATE sites SET expires_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		nullMS(expiresAt), toMS(now), siteID)
	return expectAffected(res, err, "set site expiry")
}

// ListExpiredSites returns active sites whose expiry is at or before now.
func (db *DB) ListExpiredSites(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT id FROM sites
		WHERE expires_at IS NOT NULL AND deleted_at IS NULL AND expires_at <= ? ORDER BY expires_at, id`, toMS(now))
	if err != nil {
		return nil, fmt.Errorf("list expired sites: %w", err)
	}
	return collectStrings(rows, "list expired sites")
}

// GetUserLimits returns the user's overrides; a user without any has all nil.
func (db *DB) GetUserLimits(ctx context.Context, userID int64) (domain.LimitOverrides, error) {
	var site, file, files, sites, keep sql.NullInt64
	err := db.r.QueryRowContext(ctx, `SELECT max_site_bytes, max_file_bytes, max_files, max_sites_per_user, keep_releases
		FROM user_limits WHERE user_id = ?`, userID).Scan(&site, &file, &files, &sites, &keep)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LimitOverrides{}, nil
	}
	if err != nil {
		return domain.LimitOverrides{}, fmt.Errorf("get user limits: %w", err)
	}
	return domain.LimitOverrides{
		MaxSiteBytes: fromNullInt(site), MaxFileBytes: fromNullInt(file), MaxFiles: fromNullInt(files),
		MaxSitesPerUser: fromNullInt(sites), KeepReleases: fromNullInt(keep),
	}, nil
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// SetUserLimits replaces the user's overrides; when none is set the row is removed.
func (db *DB) SetUserLimits(ctx context.Context, userID int64, o domain.LimitOverrides, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM users WHERE id = ?", userID).Scan(&one); err != nil {
			return mapNoRows(err, "check user")
		}
		if o.Empty() {
			if _, err := tx.ExecContext(ctx, "DELETE FROM user_limits WHERE user_id = ?", userID); err != nil {
				return fmt.Errorf("delete user limits: %w", err)
			}
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO user_limits
			(user_id, max_site_bytes, max_file_bytes, max_files, max_sites_per_user, keep_releases, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET max_site_bytes = excluded.max_site_bytes,
				max_file_bytes = excluded.max_file_bytes, max_files = excluded.max_files,
				max_sites_per_user = excluded.max_sites_per_user, keep_releases = excluded.keep_releases,
				updated_at = excluded.updated_at`,
			userID, nullInt(o.MaxSiteBytes), nullInt(o.MaxFileBytes), nullInt(o.MaxFiles),
			nullInt(o.MaxSitesPerUser), nullInt(o.KeepReleases), toMS(now))
		if err != nil {
			return fmt.Errorf("set user limits: %w", err)
		}
		return nil
	})
}

// EnsureSecret inserts value under name unless one exists and returns the
// stored value, so concurrent first starts agree on one secret.
func (db *DB) EnsureSecret(ctx context.Context, name string, value []byte, now time.Time) ([]byte, error) {
	var out []byte
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_secrets (name, value, created_at) VALUES (?, ?, ?)
			ON CONFLICT (name) DO NOTHING`, name, value, toMS(now)); err != nil {
			return fmt.Errorf("insert secret: %w", err)
		}
		if err := tx.QueryRowContext(ctx, "SELECT value FROM instance_secrets WHERE name = ?", name).Scan(&out); err != nil {
			return fmt.Errorf("read secret: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
