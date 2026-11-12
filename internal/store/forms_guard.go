package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ConsumeVerifyAttempt counts one confirmation attempt in a single UPDATE,
// so concurrent guesses cannot exceed max between a read and a write.
func (db *DB) ConsumeVerifyAttempt(ctx context.Context, siteID string, id int64, maxAttempts int, now time.Time) (domain.FormChannel, bool, error) {
	row := db.w.QueryRowContext(ctx, `UPDATE form_channels SET verify_attempts = verify_attempts + 1, updated_at = ?
		WHERE id = ? AND site_id = ? AND status = 'pending' AND verify_attempts < ?
		RETURNING `+channelCols, toMS(now), id, siteID, maxAttempts)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.FormChannel{}, false, nil
	}
	if err != nil {
		return domain.FormChannel{}, false, fmt.Errorf("consume verify attempt: %w", err)
	}
	ch.VerifyAttempts-- // report the state before this attempt
	return ch, true, nil
}

// ActivateFormChannel confirms a pending channel.
func (db *DB) ActivateFormChannel(ctx context.Context, siteID string, id int64, now time.Time) (bool, error) {
	res, err := db.w.ExecContext(ctx, `UPDATE form_channels SET status = 'active', verify_hash = NULL,
		verify_expires_at = NULL, verify_attempts = 0, updated_at = ?
		WHERE id = ? AND site_id = ? AND status = 'pending'`, toMS(now), id, siteID)
	n, err := affected(res, err, "activate form channel")
	return n == 1, err
}

// ResumeFormChannel clears the circuit breaker of a channel.
func (db *DB) ResumeFormChannel(ctx context.Context, siteID string, id int64, now time.Time) error {
	res, err := db.w.ExecContext(ctx, `UPDATE form_channels SET paused_at = NULL, consecutive_failures = 0, updated_at = ?
		WHERE id = ? AND site_id = ?`, toMS(now), id, siteID)
	return expectAffected(res, err, "resume form channel")
}

// RecordChannelResult updates the channel's run of failures atomically.
func (db *DB) RecordChannelResult(ctx context.Context, channelID int64, ok bool, pauseAfter int, now time.Time) (bool, error) {
	if ok {
		_, err := db.w.ExecContext(ctx, `UPDATE form_channels SET consecutive_failures = 0
			WHERE id = ? AND consecutive_failures <> 0`, channelID)
		if err != nil {
			return false, fmt.Errorf("record channel success: %w", err)
		}
		return false, nil
	}
	var paused sql.NullInt64
	var failures int
	err := db.w.QueryRowContext(ctx, `UPDATE form_channels SET consecutive_failures = consecutive_failures + 1,
		paused_at = CASE WHEN paused_at IS NULL AND ? > 0 AND consecutive_failures + 1 >= ? THEN ? ELSE paused_at END
		WHERE id = ? RETURNING consecutive_failures, paused_at`,
		pauseAfter, pauseAfter, toMS(now), channelID).Scan(&failures, &paused)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // the channel was removed meanwhile
	}
	if err != nil {
		return false, fmt.Errorf("record channel failure: %w", err)
	}
	return paused.Valid && pauseAfter > 0 && failures == pauseAfter, nil
}

// ListSubmissionsBefore pages through submissions by descending ID.
func (db *DB) ListSubmissionsBefore(ctx context.Context, siteID, form string, beforeID int64, limit int) ([]domain.FormSubmission, error) {
	where, args := "site_id = ? AND id < ?", []any{siteID, beforeID}
	if form != "" {
		where += " AND form = ?"
		args = append(args, form)
	}
	rows, err := db.r.QueryContext(ctx, "SELECT "+submissionCols+" FROM form_submissions WHERE "+where+ //nolint:gosec // G202: where is built from constants
		" ORDER BY id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("list form submissions: %w", err)
	}
	defer rows.Close()
	var out []domain.FormSubmission
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, fmt.Errorf("scan form submission: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list form submissions: %w", err)
	}
	return out, nil
}
