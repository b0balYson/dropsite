package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ReportRepository stores the quality reports of releases as JSON.
type ReportRepository interface {
	// PutDeployReport stores (or replaces) the report of a release.
	PutDeployReport(ctx context.Context, siteID, deployID string, report []byte, now time.Time) error
	// GetDeployReport returns the stored report; none → ErrNotFound.
	GetDeployReport(ctx context.Context, siteID, deployID string) ([]byte, error)
	// ListDeployScores returns the score and grade of every release of the
	// site that has a report, by deploy ID.
	ListDeployScores(ctx context.Context, siteID string) (map[string]domain.QualityScore, error)
	// DeleteDeployReports removes the reports of the given releases.
	DeleteDeployReports(ctx context.Context, siteID string, deployIDs []string) error
}

var _ ReportRepository = (*DB)(nil)

// PutDeployReport stores (or replaces) the report of a release.
func (db *DB) PutDeployReport(ctx context.Context, siteID, deployID string, report []byte, now time.Time) error {
	_, err := db.w.ExecContext(ctx, `INSERT INTO deploy_reports (site_id, deploy_id, report, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT (site_id, deploy_id) DO UPDATE SET report = excluded.report,
		created_at = excluded.created_at`, siteID, deployID, string(report), toMS(now))
	if err != nil {
		return fmt.Errorf("put deploy report: %w", err)
	}
	return nil
}

// GetDeployReport returns the stored report of a release.
func (db *DB) GetDeployReport(ctx context.Context, siteID, deployID string) ([]byte, error) {
	var s string
	err := db.r.QueryRowContext(ctx, "SELECT report FROM deploy_reports WHERE site_id = ? AND deploy_id = ?",
		siteID, deployID).Scan(&s)
	if err != nil {
		return nil, mapNoRows(err, "get deploy report")
	}
	return []byte(s), nil
}

// ListDeployScores returns score and grade by deploy ID for the site.
func (db *DB) ListDeployScores(ctx context.Context, siteID string) (map[string]domain.QualityScore, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT deploy_id, json_extract(report, '$.score'), json_extract(report, '$.grade')
		FROM deploy_reports WHERE site_id = ?`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list deploy scores: %w", err)
	}
	defer rows.Close()
	out := map[string]domain.QualityScore{}
	for rows.Next() {
		var id string
		var score sql.NullInt64
		var grade sql.NullString
		if err := rows.Scan(&id, &score, &grade); err != nil {
			return nil, fmt.Errorf("scan deploy score: %w", err)
		}
		if score.Valid && grade.Valid {
			out[id] = domain.QualityScore{Score: int(score.Int64), Grade: grade.String}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deploy scores: %w", err)
	}
	return out, nil
}

// DeleteDeployReports removes the reports of the given releases.
func (db *DB) DeleteDeployReports(ctx context.Context, siteID string, deployIDs []string) error {
	if len(deployIDs) == 0 {
		return nil
	}
	list, err := jsonList(deployIDs)
	if err != nil {
		return err
	}
	if _, err := db.w.ExecContext(ctx, `DELETE FROM deploy_reports
		WHERE site_id = ? AND deploy_id IN (SELECT value FROM json_each(?))`, siteID, list); err != nil {
		return fmt.Errorf("delete deploy reports: %w", err)
	}
	return nil
}
