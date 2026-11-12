package store

import (
	"context"
	"fmt"
)

// GetSettings returns every admin override.
func (db *DB) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	return out, nil
}

// SetSetting inserts or replaces one override.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	if _, err := db.w.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// DeleteSetting removes one override; a missing key is not an error.
func (db *DB) DeleteSetting(ctx context.Context, key string) error {
	if _, err := db.w.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key); err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return nil
}
