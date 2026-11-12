package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/b0balYson/dropsite/internal/domain"
)

// PublishRepository stores per-site publish settings.
type PublishRepository interface {
	// GetPublishSettings returns the site's settings (defaults if unset).
	GetPublishSettings(ctx context.Context, siteID string) (domain.PublishSettings, error)
	// SetPublishSettings upserts the settings of an active site.
	SetPublishSettings(ctx context.Context, s domain.PublishSettings) error
}

var _ PublishRepository = (*DB)(nil)

// GetPublishSettings returns the site's publish settings (defaults if unset).
func (db *DB) GetPublishSettings(ctx context.Context, siteID string) (domain.PublishSettings, error) {
	s := domain.DefaultPublishSettings(siteID)
	err := db.r.QueryRowContext(ctx, "SELECT optimize FROM site_publish_settings WHERE site_id = ?", siteID).
		Scan(&s.Optimize)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, fmt.Errorf("get publish settings: %w", err)
	}
	return s, nil
}

// SetPublishSettings upserts the publish settings of an active site.
func (db *DB) SetPublishSettings(ctx context.Context, s domain.PublishSettings) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, s.SiteID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO site_publish_settings (site_id, optimize) VALUES (?, ?)
			ON CONFLICT (site_id) DO UPDATE SET optimize = excluded.optimize`, s.SiteID, s.Optimize)
		if err != nil {
			return fmt.Errorf("set publish settings: %w", err)
		}
		return nil
	})
}

// deletePublishData removes the publish rows of a purged site.
func deletePublishData(ctx context.Context, tx *sql.Tx, siteID string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM site_publish_settings WHERE site_id = ?", siteID); err != nil {
		return fmt.Errorf("delete publish settings: %w", err)
	}
	return nil
}
