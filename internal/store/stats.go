package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/stats"
)

// StatsLimits bound the distinct page and referrer rows stored per site and
// day; views beyond them are added to the stats.Other row.
type StatsLimits struct {
	Pages     int
	Referrers int
}

// StatsCursor returns the stored access-log position (zero if none).
func (db *DB) StatsCursor(ctx context.Context) (stats.Cursor, error) {
	var ino, off int64
	err := db.r.QueryRowContext(ctx, "SELECT inode, byte_offset FROM stats_cursor WHERE id = 1").Scan(&ino, &off)
	if errors.Is(err, sql.ErrNoRows) {
		return stats.Cursor{}, nil
	}
	if err != nil {
		return stats.Cursor{}, fmt.Errorf("read stats cursor: %w", err)
	}
	return stats.Cursor{Inode: uint64(ino), Offset: off}, nil //nolint:gosec // inode round-trips through int64 bits
}

// SaveStatsBatch adds the batch to the aggregates and stores the new log
// position in the same transaction, so a crash never counts a line twice.
func (db *DB) SaveStatsBatch(ctx context.Context, b *stats.Batch, next stats.Cursor, lim StatsLimits, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		for k, v := range b.Views {
			if _, err := tx.ExecContext(ctx, `INSERT INTO stats_daily (site_id, day, views) VALUES (?, ?, ?)
				ON CONFLICT (site_id, day) DO UPDATE SET views = views + excluded.views`, k.SiteID, k.Day, v); err != nil {
				return fmt.Errorf("add daily views: %w", err)
			}
		}
		if err := addVisitors(ctx, tx, b.Visitors); err != nil {
			return err
		}
		if err := addNamed(ctx, tx, pageQueries, b.Pages, lim.Pages); err != nil {
			return err
		}
		if err := addNamed(ctx, tx, referrerQueries, b.Referrers, lim.Referrers); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stats_cursor (id, inode, byte_offset, updated_at) VALUES (1, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET inode = excluded.inode, byte_offset = excluded.byte_offset,
			updated_at = excluded.updated_at`, int64(next.Inode), next.Offset, toMS(now)); err != nil { //nolint:gosec // inode bits
			return fmt.Errorf("save stats cursor: %w", err)
		}
		return nil
	})
}

func addVisitors(ctx context.Context, tx *sql.Tx, visitors map[stats.Key]map[string]struct{}) error {
	for k, hashes := range visitors {
		var added int64
		for h := range hashes {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO stats_visitors (site_id, day, hash) VALUES (?, ?, ?)`,
				k.SiteID, k.Day, []byte(h))
			if err != nil {
				return fmt.Errorf("add visitor: %w", err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("add visitor: %w", err)
			}
			added += n
		}
		if added == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stats_daily (site_id, day, visitors) VALUES (?, ?, ?)
			ON CONFLICT (site_id, day) DO UPDATE SET visitors = visitors + excluded.visitors`,
			k.SiteID, k.Day, added); err != nil {
			return fmt.Errorf("add daily visitors: %w", err)
		}
	}
	return nil
}

// namedQueries are the statements of the per-name tables (pages, referrers).
type namedQueries struct {
	table, update, insert, count string
}

var (
	pageQueries = namedQueries{
		table:  "stats_pages",
		update: `UPDATE stats_pages SET views = views + ? WHERE site_id = ? AND day = ? AND path = ?`,
		insert: `INSERT INTO stats_pages (site_id, day, path, views) VALUES (?, ?, ?, ?)
			ON CONFLICT DO UPDATE SET views = views + excluded.views`,
		count: `SELECT count(*) FROM stats_pages WHERE site_id = ? AND day = ? AND path <> ?`,
	}
	referrerQueries = namedQueries{
		table:  "stats_referrers",
		update: `UPDATE stats_referrers SET views = views + ? WHERE site_id = ? AND day = ? AND host = ?`,
		insert: `INSERT INTO stats_referrers (site_id, day, host, views) VALUES (?, ?, ?, ?)
			ON CONFLICT DO UPDATE SET views = views + excluded.views`,
		count: `SELECT count(*) FROM stats_referrers WHERE site_id = ? AND day = ? AND host <> ?`,
	}
)

// addNamed upserts per-name counters, folding names beyond limit rows per
// site and day into stats.Other.
func addNamed(ctx context.Context, tx *sql.Tx, q namedQueries, m map[stats.Key]map[string]int64, limit int) error {
	table, update, insert, count := q.table, q.update, q.insert, q.count
	for k, names := range m {
		rows := -1
		for name, v := range names {
			res, err := tx.ExecContext(ctx, update, v, k.SiteID, k.Day, name)
			if err != nil {
				return fmt.Errorf("update %s: %w", table, err)
			}
			if n, err := res.RowsAffected(); err != nil {
				return fmt.Errorf("update %s: %w", table, err)
			} else if n > 0 {
				continue
			}
			if rows < 0 {
				if err := tx.QueryRowContext(ctx, count, k.SiteID, k.Day, stats.Other).Scan(&rows); err != nil {
					return fmt.Errorf("count %s: %w", table, err)
				}
			}
			target := name
			if name != stats.Other {
				if rows >= limit {
					target = stats.Other
				} else {
					rows++
				}
			}
			if _, err := tx.ExecContext(ctx, insert, k.SiteID, k.Day, target, v); err != nil {
				return fmt.Errorf("insert %s: %w", table, err)
			}
		}
	}
	return nil
}

// StatsSalt returns the visitor salt of day. When it is missing and create is
// set, a new one is generated and stored; otherwise nil is returned.
func (db *DB) StatsSalt(ctx context.Context, day string, create bool) ([]byte, error) {
	var salt []byte
	err := db.w.QueryRowContext(ctx, "SELECT salt FROM stats_salts WHERE day = ?", day).Scan(&salt)
	if err == nil {
		return salt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read salt: %w", err)
	}
	if !create {
		return nil, nil
	}
	fresh, err := stats.NewSalt()
	if err != nil {
		return nil, err
	}
	if _, err := db.w.ExecContext(ctx, "INSERT OR IGNORE INTO stats_salts (day, salt) VALUES (?, ?)", day, fresh); err != nil {
		return nil, fmt.Errorf("store salt: %w", err)
	}
	return db.StatsSalt(ctx, day, false)
}

// DeleteVisitorData removes salts and visitor hashes of days before day.
func (db *DB) DeleteVisitorData(ctx context.Context, before string) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{"DELETE FROM stats_salts WHERE day < ?", "DELETE FROM stats_visitors WHERE day < ?"} {
			if _, err := tx.ExecContext(ctx, q, before); err != nil {
				return fmt.Errorf("delete visitor data: %w", err)
			}
		}
		return nil
	})
}

// DeleteStatsBefore removes aggregates of days before day (retention).
func (db *DB) DeleteStatsBefore(ctx context.Context, before string) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			"DELETE FROM stats_daily WHERE day < ?",
			"DELETE FROM stats_pages WHERE day < ?",
			"DELETE FROM stats_referrers WHERE day < ?",
		} {
			if _, err := tx.ExecContext(ctx, q, before); err != nil {
				return fmt.Errorf("delete old statistics: %w", err)
			}
		}
		return nil
	})
}

// deleteSiteStats removes everything statistics and counters keep about a site.
func deleteSiteStats(ctx context.Context, tx *sql.Tx, siteID string) error {
	for _, q := range []string{
		"DELETE FROM stats_daily WHERE site_id = ?",
		"DELETE FROM stats_pages WHERE site_id = ?",
		"DELETE FROM stats_referrers WHERE site_id = ?",
		"DELETE FROM stats_visitors WHERE site_id = ?",
		"DELETE FROM site_counters WHERE site_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, siteID); err != nil {
			return fmt.Errorf("delete site statistics: %w", err)
		}
	}
	return nil
}

// StatsDaily returns the stored days of a site within [from, to], oldest first.
func (db *DB) StatsDaily(ctx context.Context, siteID, from, to string) ([]domain.DayStat, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT day, views, visitors FROM stats_daily
		WHERE site_id = ? AND day >= ? AND day <= ? ORDER BY day`, siteID, from, to)
	if err != nil {
		return nil, fmt.Errorf("list daily stats: %w", err)
	}
	defer rows.Close()
	var out []domain.DayStat
	for rows.Next() {
		var d domain.DayStat
		if err := rows.Scan(&d.Day, &d.Views, &d.Visitors); err != nil {
			return nil, fmt.Errorf("scan daily stats: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list daily stats: %w", err)
	}
	return out, nil
}

const (
	topPagesQuery = `SELECT path, sum(views) AS v FROM stats_pages
		WHERE site_id = ? AND day >= ? AND day <= ? GROUP BY path ORDER BY v DESC, path LIMIT ?`
	topReferrersQuery = `SELECT host, sum(views) AS v FROM stats_referrers
		WHERE site_id = ? AND day >= ? AND day <= ? GROUP BY host ORDER BY v DESC, host LIMIT ?`
)

// StatsTopPages returns the n most viewed paths of a site within [from, to].
func (db *DB) StatsTopPages(ctx context.Context, siteID, from, to string, n int) ([]domain.NamedCount, error) {
	return db.statsTop(ctx, "stats_pages", topPagesQuery, siteID, from, to, n)
}

// StatsTopReferrers returns the n biggest referrer hosts ("" = direct).
func (db *DB) StatsTopReferrers(ctx context.Context, siteID, from, to string, n int) ([]domain.NamedCount, error) {
	return db.statsTop(ctx, "stats_referrers", topReferrersQuery, siteID, from, to, n)
}

func (db *DB) statsTop(ctx context.Context, table, query, siteID, from, to string, n int) ([]domain.NamedCount, error) {
	rows, err := db.r.QueryContext(ctx, query, siteID, from, to, n)
	if err != nil {
		return nil, fmt.Errorf("top %s: %w", table, err)
	}
	defer rows.Close()
	out := []domain.NamedCount{}
	for rows.Next() {
		var c domain.NamedCount
		if err := rows.Scan(&c.Name, &c.Views); err != nil {
			return nil, fmt.Errorf("scan %s: %w", table, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("top %s: %w", table, err)
	}
	return out, nil
}

// SitesViews sums views per site within [from, to]; sites without views are absent.
func (db *DB) SitesViews(ctx context.Context, siteIDs []string, from, to string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(siteIDs) == 0 {
		return out, nil
	}
	ids, err := json.Marshal(siteIDs)
	if err != nil {
		return nil, fmt.Errorf("encode site ids: %w", err)
	}
	rows, err := db.r.QueryContext(ctx, `SELECT site_id, sum(views) FROM stats_daily
		WHERE day >= ? AND day <= ? AND site_id IN (SELECT value FROM json_each(?)) GROUP BY site_id`,
		from, to, string(ids))
	if err != nil {
		return nil, fmt.Errorf("sum site views: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var v int64
		if err := rows.Scan(&id, &v); err != nil {
			return nil, fmt.Errorf("scan site views: %w", err)
		}
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sum site views: %w", err)
	}
	return out, nil
}

// GetSiteCounters returns the site's analytics IDs (empty if none are set).
func (db *DB) GetSiteCounters(ctx context.Context, siteID string) (domain.Counters, error) {
	var c domain.Counters
	err := db.r.QueryRowContext(ctx, "SELECT metrika_id, ga4_id FROM site_counters WHERE site_id = ?", siteID).
		Scan(&c.MetrikaID, &c.GA4ID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Counters{}, nil
	}
	if err != nil {
		return domain.Counters{}, fmt.Errorf("read site counters: %w", err)
	}
	return c, nil
}

// SetSiteCounters stores the site's analytics IDs; empty counters remove the row.
func (db *DB) SetSiteCounters(ctx context.Context, siteID string, c domain.Counters, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, siteID); err != nil {
			return err
		}
		if c.Empty() {
			if _, err := tx.ExecContext(ctx, "DELETE FROM site_counters WHERE site_id = ?", siteID); err != nil {
				return fmt.Errorf("clear site counters: %w", err)
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_counters (site_id, metrika_id, ga4_id, updated_at)
			VALUES (?, ?, ?, ?) ON CONFLICT (site_id) DO UPDATE SET metrika_id = excluded.metrika_id,
			ga4_id = excluded.ga4_id, updated_at = excluded.updated_at`,
			siteID, c.MetrikaID, c.GA4ID, toMS(now)); err != nil {
			return fmt.Errorf("save site counters: %w", err)
		}
		return nil
	})
}
