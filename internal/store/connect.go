package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ConnectRepository manages one-time connect codes.
type ConnectRepository interface {
	CreateConnectCode(ctx context.Context, c domain.ConnectCode) (int64, error)
	// RedeemConnectCode marks the code with this hash used if it is unused and
	// not expired at now, and returns its user ID; otherwise ErrNotFound.
	RedeemConnectCode(ctx context.Context, hash []byte, now time.Time) (int64, error)
	DeleteExpiredConnectCodes(ctx context.Context, before time.Time) (int, error)
}

// CreateConnectCode stores a connect code and returns its ID.
func (db *DB) CreateConnectCode(ctx context.Context, c domain.ConnectCode) (int64, error) {
	res, err := db.w.ExecContext(ctx, `INSERT INTO connect_codes (user_id, hash, created_at, expires_at)
		VALUES (?, ?, ?, ?)`, c.UserID, c.Hash, toMS(c.CreatedAt), toMS(c.ExpiresAt))
	if err != nil {
		return 0, fmt.Errorf("insert connect code: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert connect code: last id: %w", err)
	}
	return id, nil
}

// RedeemConnectCode consumes a code in one statement, so two concurrent
// attempts cannot both succeed.
func (db *DB) RedeemConnectCode(ctx context.Context, hash []byte, now time.Time) (int64, error) {
	var userID int64
	err := db.w.QueryRowContext(ctx, `UPDATE connect_codes SET used_at = ?
		WHERE hash = ? AND used_at IS NULL AND expires_at > ? RETURNING user_id`,
		toMS(now), hash, toMS(now)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("redeem connect code: %w", err)
	}
	return userID, nil
}

// DeleteExpiredConnectCodes removes codes that expired before the given time.
func (db *DB) DeleteExpiredConnectCodes(ctx context.Context, before time.Time) (int, error) {
	res, err := db.w.ExecContext(ctx, "DELETE FROM connect_codes WHERE expires_at < ?", toMS(before))
	return affected(res, err, "delete expired connect codes")
}
