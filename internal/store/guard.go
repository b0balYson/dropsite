package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ProxyBudgetRepository counts proxy calls per endpoint and UTC day.
type ProxyBudgetRepository interface {
	// AddProxyDailyCall counts one call of endpoint id on day and returns
	// the day's total including it.
	AddProxyDailyCall(ctx context.Context, id int64, day string) (int, error)
	// ProxyDailyCalls returns the calls of day per endpoint of the site.
	ProxyDailyCalls(ctx context.Context, siteID, day string) (map[int64]int, error)
	// MarkProxyBudgetNotified reports true only for the first call per
	// endpoint and day, so the owner is told once.
	MarkProxyBudgetNotified(ctx context.Context, id int64, day string) (bool, error)
	// DeleteProxyDailyBefore drops the counters of days before day.
	DeleteProxyDailyBefore(ctx context.Context, day string) error
}

// BanRepository stores IP bans (abuse protection).
type BanRepository interface {
	// ListBans returns every row, active or within its memory period,
	// newest first.
	ListBans(ctx context.Context) ([]domain.IPBan, error)
	// GetBan returns the row of key or ErrNotFound.
	GetBan(ctx context.Context, key string) (domain.IPBan, error)
	// PutBan inserts or replaces the row of b.Key.
	PutBan(ctx context.Context, b domain.IPBan) error
	DeleteBan(ctx context.Context, key string) error
	// DeleteBansExpiredBefore drops rows whose ban ended before t.
	DeleteBansExpiredBefore(ctx context.Context, t time.Time) (int, error)
}

// AddProxyDailyCall implements ProxyBudgetRepository.
func (db *DB) AddProxyDailyCall(ctx context.Context, id int64, day string) (int, error) {
	var n int
	err := db.w.QueryRowContext(ctx, `INSERT INTO proxy_daily_usage (endpoint_id, day, calls) VALUES (?, ?, 1)
		ON CONFLICT (endpoint_id, day) DO UPDATE SET calls = calls + 1 RETURNING calls`, id, day).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count proxy daily call: %w", err)
	}
	return n, nil
}

// ProxyDailyCalls implements ProxyBudgetRepository.
func (db *DB) ProxyDailyCalls(ctx context.Context, siteID, day string) (map[int64]int, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT u.endpoint_id, u.calls FROM proxy_daily_usage u
		JOIN proxy_endpoints e ON e.id = u.endpoint_id WHERE e.site_id = ? AND u.day = ?`, siteID, day)
	if err != nil {
		return nil, fmt.Errorf("proxy daily calls: %w", err)
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan proxy daily calls: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proxy daily calls: %w", err)
	}
	return out, nil
}

// MarkProxyBudgetNotified implements ProxyBudgetRepository.
func (db *DB) MarkProxyBudgetNotified(ctx context.Context, id int64, day string) (bool, error) {
	res, err := db.w.ExecContext(ctx, `UPDATE proxy_daily_usage SET notified = 1
		WHERE endpoint_id = ? AND day = ? AND notified = 0`, id, day)
	n, err := affected(res, err, "mark proxy budget notified")
	return n == 1, err
}

// DeleteProxyDailyBefore implements ProxyBudgetRepository.
func (db *DB) DeleteProxyDailyBefore(ctx context.Context, day string) error {
	if _, err := db.w.ExecContext(ctx, "DELETE FROM proxy_daily_usage WHERE day < ?", day); err != nil {
		return fmt.Errorf("delete proxy daily usage: %w", err)
	}
	return nil
}

const banCols = "key, level, reason, addresses, created_at, expires_at"

func scanBan(r rowScanner) (domain.IPBan, error) {
	var (
		b           domain.IPBan
		addrs       string
		created, ex int64
	)
	if err := r.Scan(&b.Key, &b.Level, &b.Reason, &addrs, &created, &ex); err != nil {
		return domain.IPBan{}, err
	}
	if err := json.Unmarshal([]byte(addrs), &b.Addresses); err != nil {
		return domain.IPBan{}, fmt.Errorf("decode ban addresses: %w", err)
	}
	b.CreatedAt, b.ExpiresAt = fromMS(created), fromMS(ex)
	return b, nil
}

// ListBans implements BanRepository.
func (db *DB) ListBans(ctx context.Context) ([]domain.IPBan, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+banCols+" FROM ip_bans ORDER BY created_at DESC, key")
	if err != nil {
		return nil, fmt.Errorf("list bans: %w", err)
	}
	defer rows.Close()
	out := []domain.IPBan{}
	for rows.Next() {
		b, err := scanBan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ban: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list bans: %w", err)
	}
	return out, nil
}

// GetBan implements BanRepository.
func (db *DB) GetBan(ctx context.Context, key string) (domain.IPBan, error) {
	b, err := scanBan(db.r.QueryRowContext(ctx, "SELECT "+banCols+" FROM ip_bans WHERE key = ?", key))
	if err != nil {
		return domain.IPBan{}, mapNoRows(err, "get ban")
	}
	return b, nil
}

// PutBan implements BanRepository.
func (db *DB) PutBan(ctx context.Context, b domain.IPBan) error {
	addrs := b.Addresses
	if addrs == nil {
		addrs = []string{}
	}
	raw, err := json.Marshal(addrs)
	if err != nil {
		return fmt.Errorf("encode ban addresses: %w", err)
	}
	_, err = db.w.ExecContext(ctx, `INSERT INTO ip_bans (`+banCols+`) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET level = excluded.level, reason = excluded.reason,
		addresses = excluded.addresses, created_at = excluded.created_at, expires_at = excluded.expires_at`,
		b.Key, b.Level, b.Reason, string(raw), toMS(b.CreatedAt), toMS(b.ExpiresAt))
	if err != nil {
		return fmt.Errorf("put ban: %w", err)
	}
	return nil
}

// DeleteBan implements BanRepository.
func (db *DB) DeleteBan(ctx context.Context, key string) error {
	res, err := db.w.ExecContext(ctx, "DELETE FROM ip_bans WHERE key = ?", key)
	return expectAffected(res, err, "delete ban")
}

// DeleteBansExpiredBefore implements BanRepository.
func (db *DB) DeleteBansExpiredBefore(ctx context.Context, t time.Time) (int, error) {
	res, err := db.w.ExecContext(ctx, "DELETE FROM ip_bans WHERE expires_at < ?", toMS(t))
	return affected(res, err, "delete expired bans")
}
