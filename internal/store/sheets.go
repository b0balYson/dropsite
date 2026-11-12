package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// SheetRepository stores links between site collections and Google Sheets.
type SheetRepository interface {
	// GetSheetLink returns the link of a collection or domain.ErrNotFound.
	GetSheetLink(ctx context.Context, siteID, collection string) (domain.SheetLink, error)
	// PutSheetLink creates the link or replaces its configuration
	// (spreadsheet, sheet, interval, next run); the last result is kept.
	// It reports whether the link is new.
	PutSheetLink(ctx context.Context, l domain.SheetLink) (bool, error)
	// DeleteSheetLink removes a link; a missing one is domain.ErrNotFound.
	DeleteSheetLink(ctx context.Context, siteID, collection string) error
	// ClaimDueSheetLinks returns up to limit links of active sites whose
	// periodic synchronization is due and moves their next run one
	// interval ahead, in one transaction.
	ClaimDueSheetLinks(ctx context.Context, now time.Time, limit int) ([]domain.SheetLink, error)
	// RecordSheetSync stores the outcome of a synchronization.
	RecordSheetSync(ctx context.Context, siteID, collection string, r domain.SheetSyncResult) error
}

const sheetCols = `site_id, collection, spreadsheet_id, sheet_gid, sheet_title, interval_minutes, next_sync_at,
	last_sync_at, last_status, last_trigger, last_rows, last_message, last_errors, created_at, updated_at`

func scanSheetLink(r rowScanner) (domain.SheetLink, error) {
	var (
		l                    domain.SheetLink
		gid                  sql.NullInt64
		next, last           sql.NullInt64
		status, errs         string
		createdAt, updatedAt int64
	)
	if err := r.Scan(&l.SiteID, &l.Collection, &l.SpreadsheetID, &gid, &l.SheetTitle, &l.IntervalMinutes, &next,
		&last, &status, &l.LastTrigger, &l.LastRows, &l.LastMessage, &errs, &createdAt, &updatedAt); err != nil {
		return domain.SheetLink{}, err
	}
	l.SheetGID = fromNullInt(gid)
	l.NextSyncAt, l.LastSyncAt = fromNullMS(next), fromNullMS(last)
	l.LastStatus = domain.SheetSyncStatus(status)
	l.LastErrors = []domain.SheetRowError{}
	if err := json.Unmarshal([]byte(errs), &l.LastErrors); err != nil {
		return domain.SheetLink{}, fmt.Errorf("decode sheet errors: %w", err)
	}
	l.CreatedAt, l.UpdatedAt = fromMS(createdAt), fromMS(updatedAt)
	return l, nil
}

// GetSheetLink implements SheetRepository.
func (db *DB) GetSheetLink(ctx context.Context, siteID, collection string) (domain.SheetLink, error) {
	l, err := scanSheetLink(db.r.QueryRowContext(ctx, "SELECT "+sheetCols+
		" FROM collection_sheets WHERE site_id = ? AND collection = ?", siteID, collection))
	if err != nil {
		return domain.SheetLink{}, mapNoRows(err, "get sheet link")
	}
	return l, nil
}

// PutSheetLink implements SheetRepository.
func (db *DB) PutSheetLink(ctx context.Context, l domain.SheetLink) (bool, error) {
	created := false
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireCollection(ctx, tx, l.SiteID, l.Collection); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM collection_sheets WHERE site_id = ? AND collection = ?",
			l.SiteID, l.Collection).Scan(&n); err != nil {
			return fmt.Errorf("find sheet link: %w", err)
		}
		created = n == 0
		_, err := tx.ExecContext(ctx, `INSERT INTO collection_sheets
			(site_id, collection, spreadsheet_id, sheet_gid, sheet_title, interval_minutes, next_sync_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (site_id, collection) DO UPDATE SET spreadsheet_id = excluded.spreadsheet_id,
				sheet_gid = excluded.sheet_gid, sheet_title = excluded.sheet_title,
				interval_minutes = excluded.interval_minutes, next_sync_at = excluded.next_sync_at,
				updated_at = excluded.updated_at`,
			l.SiteID, l.Collection, l.SpreadsheetID, nullInt(l.SheetGID), l.SheetTitle, l.IntervalMinutes,
			nullMS(l.NextSyncAt), toMS(l.CreatedAt), toMS(l.UpdatedAt))
		if err != nil {
			return fmt.Errorf("save sheet link: %w", err)
		}
		return nil
	})
	return created, err
}

// DeleteSheetLink implements SheetRepository.
func (db *DB) DeleteSheetLink(ctx context.Context, siteID, collection string) error {
	res, err := db.w.ExecContext(ctx, "DELETE FROM collection_sheets WHERE site_id = ? AND collection = ?", siteID, collection)
	return expectAffected(res, err, "delete sheet link")
}

// ClaimDueSheetLinks implements SheetRepository.
func (db *DB) ClaimDueSheetLinks(ctx context.Context, now time.Time, limit int) ([]domain.SheetLink, error) {
	var out []domain.SheetLink
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+prefixCols("c.", sheetCols)+` FROM collection_sheets c
			JOIN sites s ON s.id = c.site_id AND s.deleted_at IS NULL
			WHERE c.interval_minutes > 0 AND (c.next_sync_at IS NULL OR c.next_sync_at <= ?)
			ORDER BY c.next_sync_at LIMIT ?`, toMS(now), limit)
		if err != nil {
			return fmt.Errorf("list due sheet links: %w", err)
		}
		for rows.Next() {
			l, err := scanSheetLink(rows)
			if err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan sheet link: %w", err)
			}
			out = append(out, l)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("list due sheet links: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list due sheet links: %w", err)
		}
		for i, l := range out {
			next := now.Add(time.Duration(l.IntervalMinutes) * time.Minute)
			if _, err := tx.ExecContext(ctx, "UPDATE collection_sheets SET next_sync_at = ? WHERE site_id = ? AND collection = ?",
				toMS(next), l.SiteID, l.Collection); err != nil {
				return fmt.Errorf("claim sheet link: %w", err)
			}
			out[i].NextSyncAt = &next
		}
		return nil
	})
	return out, err
}

// RecordSheetSync implements SheetRepository.
func (db *DB) RecordSheetSync(ctx context.Context, siteID, collection string, r domain.SheetSyncResult) error {
	errs := r.Errors
	if errs == nil {
		errs = []domain.SheetRowError{}
	}
	b, err := json.Marshal(errs)
	if err != nil {
		return fmt.Errorf("encode sheet errors: %w", err)
	}
	res, err := db.w.ExecContext(ctx, `UPDATE collection_sheets SET last_sync_at = ?, last_status = ?, last_trigger = ?,
		last_rows = ?, last_message = ?, last_errors = ?,
		sheet_title = CASE WHEN ? = '' THEN sheet_title ELSE ? END
		WHERE site_id = ? AND collection = ?`,
		toMS(r.At), string(r.Status), r.Trigger, r.Rows, r.Message, string(b), r.SheetTitle, r.SheetTitle, siteID, collection)
	return expectAffected(res, err, "record sheet sync")
}

// prefixCols qualifies a comma-separated column list with a table alias.
func prefixCols(prefix, cols string) string {
	out := []byte{}
	start := true
	for i := 0; i < len(cols); i++ {
		c := cols[i]
		if start && c != ' ' && c != '\n' && c != '\t' {
			out = append(out, prefix...)
			start = false
		}
		out = append(out, c)
		if c == ',' {
			start = true
		}
	}
	return string(out)
}
