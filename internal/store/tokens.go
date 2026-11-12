package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const tokenCols = //nolint:gosec // G101: a column list, not a credential
"id, user_id, name, prefix, hash, created_at, last_used_at, expires_at, revoked_at"

func scanToken(s rowScanner) (domain.Token, error) {
	var t domain.Token
	var created int64
	var used, expires, revoked sql.NullInt64
	if err := s.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.Hash, &created, &used, &expires, &revoked); err != nil {
		return domain.Token{}, err
	}
	t.CreatedAt = fromMS(created)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = fromNullMS(used), fromNullMS(expires), fromNullMS(revoked)
	return t, nil
}

// CreateToken stores an API token and returns its ID.
func (db *DB) CreateToken(ctx context.Context, t domain.Token) (int64, error) {
	res, err := db.w.ExecContext(ctx, `INSERT INTO tokens
		(user_id, name, prefix, hash, created_at, last_used_at, expires_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.Name, t.Prefix, t.Hash, toMS(t.CreatedAt), nullMS(t.LastUsedAt), nullMS(t.ExpiresAt), nullMS(t.RevokedAt))
	if err != nil {
		return 0, fmt.Errorf("insert token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert token: last id: %w", err)
	}
	return id, nil
}

// GetTokenByHash loads a token by the SHA-256 of its value.
func (db *DB) GetTokenByHash(ctx context.Context, hash []byte) (domain.Token, error) {
	t, err := scanToken(db.r.QueryRowContext(ctx, "SELECT "+tokenCols+" FROM tokens WHERE hash = ?", hash))
	if err != nil {
		return domain.Token{}, mapNoRows(err, "get token")
	}
	return t, nil
}

// ListTokens returns the user's tokens, revoked included, newest first.
func (db *DB) ListTokens(ctx context.Context, userID int64) ([]domain.Token, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+tokenCols+
		" FROM tokens WHERE user_id = ? ORDER BY created_at DESC, id DESC", userID)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	out := []domain.Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	return out, nil
}

// RevokeToken revokes the user's token; a foreign or missing token → ErrNotFound.
// Revoking an already revoked token keeps the original revocation time.
func (db *DB) RevokeToken(ctx context.Context, userID, tokenID int64, now time.Time) error {
	res, err := db.w.ExecContext(ctx, "UPDATE tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND user_id = ?",
		toMS(now), tokenID, userID)
	return expectAffected(res, err, "revoke token")
}

// RevokeUserTokens revokes all active tokens of a user.
func (db *DB) RevokeUserTokens(ctx context.Context, userID int64, now time.Time) (int, error) {
	res, err := db.w.ExecContext(ctx, "UPDATE tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		toMS(now), userID)
	if err != nil {
		return 0, fmt.Errorf("revoke user tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke user tokens: %w", err)
	}
	return int(n), nil
}

// TouchToken records the last use of a token.
func (db *DB) TouchToken(ctx context.Context, tokenID int64, now time.Time) error {
	if _, err := db.w.ExecContext(ctx, "UPDATE tokens SET last_used_at = ? WHERE id = ?", toMS(now), tokenID); err != nil {
		return fmt.Errorf("touch token: %w", err)
	}
	return nil
}
