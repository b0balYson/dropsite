package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// errDomainLimit is returned with the limit attached by AddExternalHost.
var errDomainLimit = domain.E(domain.KindLimit, domain.CodeDomainLimit, "external domain limit reached")

const externalCols = "h.hostname, h.site_id, h.kind, h.verified, h.created_at, h.reject_reason, h.reviewed_at, h.reviewed_by"

// externalFrom joins external hostnames of active sites with site and owner.
const externalFrom = ` FROM hostnames h JOIN sites s ON s.id = h.site_id JOIN users u ON u.id = s.owner_id
	WHERE h.kind = 'external' AND s.deleted_at IS NULL`

// externalStatusWhere filters by derived status (?1: ” = any) and by a LIKE
// pattern (?2: ” = none) over hostname, site title and owner email.
const externalStatusWhere = ` AND (?1 = ''
		OR (?1 = 'active' AND h.verified = 1)
		OR (?1 = 'pending' AND h.verified = 0 AND h.reviewed_at IS NULL)
		OR (?1 = 'rejected' AND h.verified = 0 AND h.reviewed_at IS NOT NULL))
	AND (?2 = '' OR h.hostname LIKE ?2 ESCAPE '\' OR s.title LIKE ?2 ESCAPE '\' OR u.email LIKE ?2 ESCAPE '\')`

// AddExternalHost inserts an unverified external hostname for an active site,
// unless the site already has maxPerSite external hostnames (any status).
func (db *DB) AddExternalHost(ctx context.Context, h domain.Hostname, maxPerSite int) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, h.SiteID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM hostnames WHERE site_id = ? AND kind = 'external'",
			h.SiteID).Scan(&n); err != nil {
			return fmt.Errorf("count external hostnames: %w", err)
		}
		if n >= maxPerSite {
			return errDomainLimit.WithParams(map[string]any{"limit": maxPerSite})
		}
		h.Kind, h.Verified, h.RejectReason, h.ReviewedAt, h.ReviewedBy = domain.HostExternal, false, "", nil, nil
		if err := insertHost(ctx, tx, h); err != nil {
			return err
		}
		return touchSite(ctx, tx, h.SiteID, h.CreatedAt)
	})
}

// RemoveExternalHost deletes an external hostname of an active site and
// returns it. Any other kind, another site's or a missing host → ErrNotFound.
func (db *DB) RemoveExternalHost(ctx context.Context, siteID, hostname string, now time.Time) (domain.Hostname, error) {
	var removed domain.Hostname
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, siteID); err != nil {
			return err
		}
		var err error
		if removed, err = loadExternal(ctx, tx, siteID, hostname); err != nil {
			return err
		}
		if err := deleteHost(ctx, tx, hostname); err != nil {
			return err
		}
		return touchSite(ctx, tx, siteID, now)
	})
	if err != nil {
		return domain.Hostname{}, err
	}
	return removed, nil
}

// ReviewExternalHost records an administrator's decision on an external
// hostname of an active site and returns the updated row.
func (db *DB) ReviewExternalHost(ctx context.Context, r ExternalReview) (domain.Hostname, error) {
	var out domain.Hostname
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, r.SiteID); err != nil {
			return err
		}
		if _, err := loadExternal(ctx, tx, r.SiteID, r.Hostname); err != nil {
			return err
		}
		reason := r.Reason
		if r.Approve {
			reason = ""
		}
		var reviewer any
		if r.ReviewerID != 0 {
			reviewer = r.ReviewerID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE hostnames SET verified = ?, reject_reason = ?, reviewed_at = ?, reviewed_by = ?
			WHERE hostname = ?`, r.Approve, reason, toMS(r.At), reviewer, r.Hostname); err != nil {
			return fmt.Errorf("review hostname: %w", err)
		}
		if err := touchSite(ctx, tx, r.SiteID, r.At); err != nil {
			return err
		}
		var err error
		out, err = loadExternal(ctx, tx, r.SiteID, r.Hostname)
		return err
	})
	if err != nil {
		return domain.Hostname{}, err
	}
	return out, nil
}

// loadExternal loads the site's external hostname or returns ErrNotFound.
func loadExternal(ctx context.Context, q querier, siteID, hostname string) (domain.Hostname, error) {
	h, err := scanHost(q.QueryRowContext(ctx, "SELECT "+hostCols+
		" FROM hostnames WHERE hostname = ? AND site_id = ? AND kind = 'external'", hostname, siteID))
	if err != nil {
		return domain.Hostname{}, mapNoRows(err, "load external hostname")
	}
	return h, nil
}

// GetExternalHost returns an external hostname of an active site with its
// site and owner.
func (db *DB) GetExternalHost(ctx context.Context, hostname string) (ExternalHost, error) {
	row := db.r.QueryRowContext(ctx, "SELECT "+externalCols+", s.title, s.owner_id, u.email"+externalFrom+
		" AND h.hostname = ?", hostname)
	e, err := scanExternal(row)
	if err != nil {
		return ExternalHost{}, mapNoRows(err, "get external hostname")
	}
	return e, nil
}

// ListExternalHosts returns a page of external hostnames of active sites,
// oldest first, and the total number of matches.
func (db *DB) ListExternalHosts(ctx context.Context, f ExternalHostFilter) ([]ExternalHost, int, error) {
	var out []ExternalHost
	var total int
	err := db.withReadTx(ctx, func(tx *sql.Tx) error {
		status, pattern := string(f.Status), likePattern(f.Query)
		if err := tx.QueryRowContext(ctx, "SELECT count(*)"+externalFrom+externalStatusWhere, status, pattern).
			Scan(&total); err != nil {
			return fmt.Errorf("count external hostnames: %w", err)
		}
		limit, offset := f.bounds()
		rows, err := tx.QueryContext(ctx, "SELECT "+externalCols+", s.title, s.owner_id, u.email"+externalFrom+
			externalStatusWhere+" ORDER BY h.created_at, h.hostname LIMIT ?3 OFFSET ?4", status, pattern, limit, offset)
		if err != nil {
			return fmt.Errorf("list external hostnames: %w", err)
		}
		defer rows.Close()
		out = []ExternalHost{}
		for rows.Next() {
			e, err := scanExternal(rows)
			if err != nil {
				return fmt.Errorf("scan external hostname: %w", err)
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list external hostnames: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func scanExternal(s rowScanner) (ExternalHost, error) {
	var e ExternalHost
	var created int64
	var reviewedAt, reviewedBy sql.NullInt64
	h := &e.Hostname
	if err := s.Scan(&h.Hostname, &h.SiteID, &h.Kind, &h.Verified, &created, &h.RejectReason,
		&reviewedAt, &reviewedBy, &e.SiteTitle, &e.OwnerID, &e.OwnerEmail); err != nil {
		return ExternalHost{}, err
	}
	h.CreatedAt = fromMS(created)
	h.ReviewedAt, h.ReviewedBy = fromNullMS(reviewedAt), fromNullInt(reviewedBy)
	return e, nil
}
