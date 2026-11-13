package store

import (
	"context"
	"fmt"
	"time"
)

// AddProxyRejected implements ProxyRepository: one write for a batch of
// calls refused before forwarding. The last call time and status move
// only forward, so a late batch never hides a newer forwarded call.
func (db *DB) AddProxyRejected(ctx context.Context, id int64, n, status int, at time.Time) error {
	if n <= 0 {
		return nil
	}
	ms := toMS(at)
	_, err := db.w.ExecContext(ctx, `UPDATE proxy_endpoints SET rejected = rejected + ?,
		last_status = CASE WHEN COALESCE(last_used_at, 0) <= ? THEN ? ELSE last_status END,
		last_used_at = MAX(COALESCE(last_used_at, 0), ?)
		WHERE id = ?`, n, ms, status, ms, id)
	if err != nil {
		return fmt.Errorf("add proxy rejected: %w", err)
	}
	return nil
}
