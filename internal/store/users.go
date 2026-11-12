package store

import (
	"context"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const userCols = "id, email, password_hash, role, status, lang, created_at, updated_at"

func scanUser(s rowScanner, extra ...any) (domain.User, error) {
	var u domain.User
	var created, updated int64
	dest := append([]any{&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Status, &u.Lang, &created, &updated}, extra...)
	if err := s.Scan(dest...); err != nil {
		return domain.User{}, err
	}
	u.CreatedAt, u.UpdatedAt = fromMS(created), fromMS(updated)
	return u, nil
}

// CountUsers returns the number of accounts.
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := db.r.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateUser inserts u; a duplicate email in any case → CodeEmailTaken.
func (db *DB) CreateUser(ctx context.Context, u domain.User) (int64, error) {
	res, err := db.w.ExecContext(ctx, `INSERT INTO users
		(email, password_hash, role, status, lang, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Email, u.PasswordHash, string(u.Role), string(u.Status), u.Lang, toMS(u.CreatedAt), toMS(u.UpdatedAt))
	if err != nil {
		return 0, mapUnique(err, errEmailTaken, "insert user")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert user: last id: %w", err)
	}
	return id, nil
}

// GetUser loads a user by ID.
func (db *DB) GetUser(ctx context.Context, id int64) (domain.User, error) {
	u, err := scanUser(db.r.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
	if err != nil {
		return domain.User{}, mapNoRows(err, "get user")
	}
	return u, nil
}

// GetUserByEmail loads a user by email, case-insensitively (column is COLLATE NOCASE).
func (db *DB) GetUserByEmail(ctx context.Context, email string) (domain.User, error) {
	u, err := scanUser(db.r.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE email = ?", email))
	if err != nil {
		return domain.User{}, mapNoRows(err, "get user by email")
	}
	return u, nil
}

const userListWhere = ` WHERE (?1 = '' OR email LIKE ?1 ESCAPE '\') AND (?2 = '' OR status = ?2)`

// ListUsers returns a page of users ordered by creation time, with active site counts.
func (db *DB) ListUsers(ctx context.Context, f UserFilter) ([]UserSummary, int, error) {
	pattern, status := likePattern(f.Query), string(f.Status)
	var total int
	if err := db.r.QueryRowContext(ctx, "SELECT count(*) FROM users"+userListWhere, pattern, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	limit, offset := f.bounds()
	rows, err := db.r.QueryContext(ctx, "SELECT "+userCols+`,
		(SELECT count(*) FROM sites s WHERE s.owner_id = users.id AND s.deleted_at IS NULL)
		FROM users`+userListWhere+` ORDER BY created_at, id LIMIT ?3 OFFSET ?4`,
		pattern, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	out := []UserSummary{}
	for rows.Next() {
		var s UserSummary
		if s.User, err = scanUser(rows, &s.SiteCount); err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return out, total, nil
}

// SetUserStatus changes the lifecycle status.
func (db *DB) SetUserStatus(ctx context.Context, id int64, status domain.UserStatus, now time.Time) error {
	return db.updateUserField(ctx, "UPDATE users SET status = ?, updated_at = ? WHERE id = ?", string(status), now, id)
}

// SetUserPassword stores a new password hash.
func (db *DB) SetUserPassword(ctx context.Context, id int64, passwordHash string, now time.Time) error {
	return db.updateUserField(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, now, id)
}

// SetUserLang stores the preferred UI language.
func (db *DB) SetUserLang(ctx context.Context, id int64, lang string, now time.Time) error {
	return db.updateUserField(ctx, "UPDATE users SET lang = ?, updated_at = ? WHERE id = ?", lang, now, id)
}

func (db *DB) updateUserField(ctx context.Context, query, value string, now time.Time, id int64) error {
	res, err := db.w.ExecContext(ctx, query, value, toMS(now), id)
	return expectAffected(res, err, "update user")
}
