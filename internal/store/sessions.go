package store

import (
	"context"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// CreateSession stores a UI session.
func (db *DB) CreateSession(ctx context.Context, s domain.Session) error {
	_, err := db.w.ExecContext(ctx, `INSERT INTO sessions
		(id, user_id, csrf_secret, created_at, expires_at, last_seen_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.CSRFSecret, toMS(s.CreatedAt), toMS(s.ExpiresAt), toMS(s.LastSeenAt), s.IP, s.UserAgent)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetSession loads a session by the hash of its cookie value.
func (db *DB) GetSession(ctx context.Context, id []byte) (domain.Session, error) {
	var s domain.Session
	var created, expires, seen int64
	err := db.r.QueryRowContext(ctx, `SELECT id, user_id, csrf_secret, created_at, expires_at, last_seen_at, ip, user_agent
		FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.UserID, &s.CSRFSecret, &created, &expires, &seen, &s.IP, &s.UserAgent)
	if err != nil {
		return domain.Session{}, mapNoRows(err, "get session")
	}
	s.CreatedAt, s.ExpiresAt, s.LastSeenAt = fromMS(created), fromMS(expires), fromMS(seen)
	return s, nil
}

// TouchSession extends a session's lifetime.
func (db *DB) TouchSession(ctx context.Context, id []byte, lastSeen, expires time.Time) error {
	if _, err := db.w.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?",
		toMS(lastSeen), toMS(expires), id); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// DeleteSession removes one session; deleting a missing session is not an error.
func (db *DB) DeleteSession(ctx context.Context, id []byte) error {
	if _, err := db.w.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions removes every session of the user.
func (db *DB) DeleteUserSessions(ctx context.Context, userID int64) error {
	if _, err := db.w.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes sessions whose expiry is at or before now.
func (db *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := db.w.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", toMS(now))
	return affected(res, err, "delete expired sessions")
}
