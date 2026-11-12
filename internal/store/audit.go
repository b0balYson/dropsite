package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/b0balYson/dropsite/internal/domain"
)

// auditWhere filters by actor (?1), action (?2, empty = any), From (?3, inclusive)
// and To (?4, exclusive); NULL disables a bound.
const auditWhere = ` WHERE (?1 IS NULL OR a.actor_id = ?1) AND (?2 = '' OR a.action = ?2)
	AND (?3 IS NULL OR a.created_at >= ?3) AND (?4 IS NULL OR a.created_at < ?4)`

// InsertAudit appends an entry; Meta is stored as JSON ("{}" when nil).
func (db *DB) InsertAudit(ctx context.Context, e domain.AuditEntry) error {
	meta := []byte("{}")
	if e.Meta != nil {
		var err error
		if meta, err = json.Marshal(e.Meta); err != nil {
			return fmt.Errorf("encode audit meta: %w", err)
		}
	}
	if _, err := db.w.ExecContext(ctx, `INSERT INTO audit_log (actor_id, action, target, meta_json, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, e.ActorID, e.Action, e.Target, string(meta), e.IP, toMS(e.CreatedAt)); err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

// ListAudit returns a page of entries, newest first, and the total match count.
func (db *DB) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, int, error) {
	args := []any{f.ActorID, f.Action, nullMS(f.From), nullMS(f.To)}
	var total int
	if err := db.r.QueryRowContext(ctx, "SELECT count(*) FROM audit_log a"+auditWhere, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit entries: %w", err)
	}
	limit, offset := f.bounds()
	rows, err := db.r.QueryContext(ctx, `SELECT a.id, a.actor_id, a.action, a.target, a.meta_json, a.ip, a.created_at,
		COALESCE(u.email, '') FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id`+auditWhere+
		` ORDER BY a.created_at DESC, a.id DESC LIMIT ?5 OFFSET ?6`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		r, err := scanAuditRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list audit entries: %w", err)
	}
	return out, total, nil
}

func scanAuditRow(rows *sql.Rows) (AuditRow, error) {
	var r AuditRow
	var actor sql.NullInt64
	var meta string
	var created int64
	if err := rows.Scan(&r.ID, &actor, &r.Action, &r.Target, &meta, &r.IP, &created, &r.ActorEmail); err != nil {
		return AuditRow{}, fmt.Errorf("scan audit entry: %w", err)
	}
	if err := json.Unmarshal([]byte(meta), &r.Meta); err != nil {
		return AuditRow{}, fmt.Errorf("decode audit meta %d: %w", r.ID, err)
	}
	r.ActorID, r.CreatedAt = fromNullInt(actor), fromMS(created)
	return r, nil
}

// ListAuditActions returns the distinct actions present in the log, sorted.
func (db *DB) ListAuditActions(ctx context.Context) ([]string, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT DISTINCT action FROM audit_log ORDER BY action")
	if err != nil {
		return nil, fmt.Errorf("list audit actions: %w", err)
	}
	return collectStrings(rows, "list audit actions")
}
