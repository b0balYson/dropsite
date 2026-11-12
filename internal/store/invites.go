package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const inviteCols = "id, user_id, hash, created_by, created_at, expires_at, used_at, revoked_at"

func scanInvite(s rowScanner) (domain.Invite, error) {
	var inv domain.Invite
	var created, expires int64
	var used, revoked sql.NullInt64
	if err := s.Scan(&inv.ID, &inv.UserID, &inv.Hash, &inv.CreatedBy, &created, &expires, &used, &revoked); err != nil {
		return domain.Invite{}, err
	}
	inv.CreatedAt, inv.ExpiresAt = fromMS(created), fromMS(expires)
	inv.UsedAt, inv.RevokedAt = fromNullMS(used), fromNullMS(revoked)
	return inv, nil
}

// CreateInvite revokes every unused invite of inv.UserID (at inv.CreatedAt)
// and inserts inv, atomically.
func (db *DB) CreateInvite(ctx context.Context, inv domain.Invite) (int64, error) {
	var id int64
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET revoked_at = ?
			WHERE user_id = ? AND used_at IS NULL AND revoked_at IS NULL`, toMS(inv.CreatedAt), inv.UserID); err != nil {
			return fmt.Errorf("revoke invites: %w", err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO invites
			(user_id, hash, created_by, created_at, expires_at, used_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			inv.UserID, inv.Hash, inv.CreatedBy, toMS(inv.CreatedAt), toMS(inv.ExpiresAt),
			nullMS(inv.UsedAt), nullMS(inv.RevokedAt))
		if err != nil {
			return fmt.Errorf("insert invite: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("insert invite: last id: %w", err)
		}
		return nil
	})
	return id, err
}

// GetInviteByHash loads an invite by the hash of its secret.
func (db *DB) GetInviteByHash(ctx context.Context, hash []byte) (domain.Invite, error) {
	inv, err := scanInvite(db.r.QueryRowContext(ctx, "SELECT "+inviteCols+" FROM invites WHERE hash = ?", hash))
	if err != nil {
		return domain.Invite{}, mapNoRows(err, "get invite")
	}
	return inv, nil
}

// RedeemInvite marks the invite used, stores the password and activates the
// user in one transaction. Missing, used, revoked or expired invites and
// blocked users → CodeInviteInvalid.
func (db *DB) RedeemInvite(ctx context.Context, inviteID int64, passwordHash string, now time.Time) (domain.User, error) {
	var u domain.User
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		userID, err := usableInviteUser(ctx, tx, inviteID, now)
		if err != nil {
			return err
		}
		nowMS := toMS(now)
		if _, err := tx.ExecContext(ctx, "UPDATE invites SET used_at = ? WHERE id = ?", nowMS, inviteID); err != nil {
			return fmt.Errorf("mark invite used: %w", err)
		}
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'active', updated_at = ?
			WHERE id = ? AND status <> 'blocked'`, passwordHash, nowMS, userID)
		if err := expectAffected(res, err, "activate user"); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return errInviteInvalid
			}
			return err
		}
		u, err = scanUser(tx.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", userID))
		if err != nil {
			return fmt.Errorf("reload user: %w", err)
		}
		return nil
	})
	return u, err
}

// usableInviteUser returns the invite's user ID if the invite can be redeemed at now.
func usableInviteUser(ctx context.Context, tx *sql.Tx, inviteID int64, now time.Time) (int64, error) {
	var userID int64
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM invites
		WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`, inviteID, toMS(now)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errInviteInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("load invite: %w", err)
	}
	return userID, nil
}

// DeleteExpiredInvites removes invites that expired before the given time.
func (db *DB) DeleteExpiredInvites(ctx context.Context, before time.Time) (int, error) {
	res, err := db.w.ExecContext(ctx, "DELETE FROM invites WHERE expires_at < ?", toMS(before))
	return affected(res, err, "delete expired invites")
}
