package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const (
	hostCols  = "hostname, site_id, kind, verified, created_at, reject_reason, reviewed_at, reviewed_by"
	hostOrder = ` ORDER BY site_id, CASE kind WHEN 'default' THEN 0 WHEN 'slug' THEN 1 WHEN 'apex' THEN 2 ELSE 3 END, hostname`
)

func scanHost(s rowScanner) (domain.Hostname, error) {
	var h domain.Hostname
	var created int64
	var reviewedAt, reviewedBy sql.NullInt64
	if err := s.Scan(&h.Hostname, &h.SiteID, &h.Kind, &h.Verified, &created,
		&h.RejectReason, &reviewedAt, &reviewedBy); err != nil {
		return domain.Hostname{}, err
	}
	h.CreatedAt = fromMS(created)
	h.ReviewedAt, h.ReviewedBy = fromNullMS(reviewedAt), fromNullInt(reviewedBy)
	return h, nil
}

// insertHost inserts h. A taken hostname, or a slug label equal to any site ID
// (tombstones included), → CodeSlugTaken (CodeDomainTaken for external hosts).
func insertHost(ctx context.Context, tx *sql.Tx, h domain.Hostname) error {
	if h.Kind == domain.HostSlug {
		if err := checkSlugLabel(ctx, tx, h.Hostname); err != nil {
			return err
		}
	}
	conflict := errSlugTaken
	if h.Kind == domain.HostExternal {
		conflict = errDomainTaken
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO hostnames ("+hostCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		h.Hostname, h.SiteID, string(h.Kind), h.Verified, toMS(h.CreatedAt),
		h.RejectReason, nullMS(h.ReviewedAt), h.ReviewedBy)
	if err != nil {
		return mapUnique(err, conflict, "insert hostname")
	}
	return nil
}

// checkSlugLabel rejects a slug whose label (text before the first dot) is a site ID.
func checkSlugLabel(ctx context.Context, tx *sql.Tx, hostname string) error {
	label, _, _ := strings.Cut(hostname, ".")
	var taken bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sites WHERE id = ?)", label).Scan(&taken); err != nil {
		return fmt.Errorf("check slug label: %w", err)
	}
	if taken {
		return errSlugTaken
	}
	return nil
}

// siteHostnames returns the names bound to the site.
func siteHostnames(ctx context.Context, q querier, siteID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT hostname FROM hostnames WHERE site_id = ?"+hostOrder, siteID)
	if err != nil {
		return nil, fmt.Errorf("list hostnames: %w", err)
	}
	return collectStrings(rows, "list hostnames")
}

// hostByKind returns the site's hostname of the given kind, or "" if none.
func hostByKind(ctx context.Context, tx *sql.Tx, siteID string, kind domain.HostKind) (string, error) {
	var h string
	err := tx.QueryRowContext(ctx, "SELECT hostname FROM hostnames WHERE site_id = ? AND kind = ?", siteID, string(kind)).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load %s hostname: %w", kind, err)
	}
	return h, nil
}

func touchSite(ctx context.Context, tx *sql.Tx, siteID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, "UPDATE sites SET updated_at = ? WHERE id = ?", toMS(now), siteID); err != nil {
		return fmt.Errorf("touch site: %w", err)
	}
	return nil
}

func deleteHost(ctx context.Context, tx *sql.Tx, hostname string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM hostnames WHERE hostname = ?", hostname); err != nil {
		return fmt.Errorf("delete hostname: %w", err)
	}
	return nil
}

// ListHostnames returns the site's hostnames in every status: default, slug,
// apex, external.
// A deleted site has none.
func (db *DB) ListHostnames(ctx context.Context, siteID string) ([]domain.Hostname, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+hostCols+" FROM hostnames WHERE site_id = ?"+hostOrder, siteID)
	if err != nil {
		return nil, fmt.Errorf("list hostnames: %w", err)
	}
	defer rows.Close()
	out := []domain.Hostname{}
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hostname: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hostnames: %w", err)
	}
	return out, nil
}

// SetSlugHost replaces the site's slug hostname with host (nil only removes it).
func (db *DB) SetSlugHost(ctx context.Context, siteID string, host *domain.Hostname, now time.Time) (string, error) {
	var removed string
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, siteID); err != nil {
			return err
		}
		var err error
		if removed, err = hostByKind(ctx, tx, siteID, domain.HostSlug); err != nil {
			return err
		}
		if removed != "" {
			if err := deleteHost(ctx, tx, removed); err != nil {
				return err
			}
		}
		if host != nil {
			h := *host
			h.SiteID, h.Kind = siteID, domain.HostSlug
			if err := insertHost(ctx, tx, h); err != nil {
				return err
			}
		}
		return touchSite(ctx, tx, siteID, now)
	})
	if err != nil {
		return "", err
	}
	return removed, nil
}

// SetApexHost binds the single apex hostname to siteID, taking it from any
// other site, and returns the previous holder ("" if none).
func (db *DB) SetApexHost(ctx context.Context, siteID string, host domain.Hostname, now time.Time) (string, error) {
	var prev string
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, siteID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, "SELECT site_id FROM hostnames WHERE kind = 'apex'").Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load apex hostname: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM hostnames WHERE kind = 'apex'"); err != nil {
			return fmt.Errorf("delete apex hostname: %w", err)
		}
		host.SiteID, host.Kind = siteID, domain.HostApex
		if err := insertHost(ctx, tx, host); err != nil {
			return err
		}
		return touchSite(ctx, tx, siteID, now)
	})
	if err != nil {
		return "", err
	}
	return prev, nil
}

// ClearApexHost removes the apex hostname if siteID holds it and returns it.
func (db *DB) ClearApexHost(ctx context.Context, siteID string, now time.Time) (string, error) {
	var removed string
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, siteID); err != nil {
			return err
		}
		var err error
		if removed, err = hostByKind(ctx, tx, siteID, domain.HostApex); err != nil || removed == "" {
			return err
		}
		if err := deleteHost(ctx, tx, removed); err != nil {
			return err
		}
		return touchSite(ctx, tx, siteID, now)
	})
	if err != nil {
		return "", err
	}
	return removed, nil
}
