package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// WorkspaceRepository stores shared workspaces, their members and the
// workspace of each site.
type WorkspaceRepository interface {
	// CreateWorkspace inserts a workspace with its creator as the only member
	// and returns its ID; a creator already in maxPerUser workspaces →
	// CodeWorkspaceLimit.
	CreateWorkspace(ctx context.Context, name string, creatorID int64, maxPerUser int, now time.Time) (int64, error)
	// GetWorkspace loads one workspace with its counts.
	GetWorkspace(ctx context.Context, id int64) (domain.Workspace, error)
	// ListWorkspaces lists the workspaces of memberID (nil: every workspace),
	// by name.
	ListWorkspaces(ctx context.Context, memberID *int64) ([]domain.Workspace, error)
	RenameWorkspace(ctx context.Context, id int64, name string, now time.Time) error
	// DeleteWorkspace deletes a workspace without active sites
	// (CodeWorkspaceNotEmpty otherwise); its deleted sites become personal.
	DeleteWorkspace(ctx context.Context, id int64) error
	IsWorkspaceMember(ctx context.Context, workspaceID, userID int64) (bool, error)
	// ListWorkspaceMembers lists members by e-mail.
	ListWorkspaceMembers(ctx context.Context, workspaceID int64) ([]domain.WorkspaceMember, error)
	// AddWorkspaceMember adds a member: already a member →
	// CodeWorkspaceMemberExists; maxMembers reached or the user already in
	// maxPerUser workspaces → CodeWorkspaceLimit.
	AddWorkspaceMember(ctx context.Context, workspaceID, userID, addedBy int64, maxMembers, maxPerUser int, now time.Time) error
	// RemoveWorkspaceMember removes a member (ErrNotFound if not one). The
	// last member of a workspace with active sites → CodeWorkspaceLastMember;
	// the last member of an empty workspace takes it along (deleted true).
	RemoveWorkspaceMember(ctx context.Context, workspaceID, userID int64) (deleted bool, err error)
	// MoveSite puts an active site into a workspace (nil: personal). When
	// memberID is set, it must still be a member of the target workspace at
	// the time of the move (ErrNotFound otherwise).
	MoveSite(ctx context.Context, siteID string, workspaceID *int64, memberID *int64, now time.Time) error
	// SiteWorkspaceID returns the workspace of any site, deleted or not (nil
	// for personal or unknown sites).
	SiteWorkspaceID(ctx context.Context, siteID string) (*int64, error)
}

var _ WorkspaceRepository = (*DB)(nil)

const workspaceCols = `w.id, w.name, w.created_by, w.created_at, w.updated_at,
	(SELECT count(*) FROM workspace_members m WHERE m.workspace_id = w.id),
	(SELECT count(*) FROM sites s WHERE s.workspace_id = w.id AND s.deleted_at IS NULL)`

func scanWorkspace(r rowScanner) (domain.Workspace, error) {
	var w domain.Workspace
	var by sql.NullInt64
	var created, updated int64
	if err := r.Scan(&w.ID, &w.Name, &by, &created, &updated, &w.MemberCount, &w.SiteCount); err != nil {
		return domain.Workspace{}, err
	}
	w.CreatedBy, w.CreatedAt, w.UpdatedAt = fromNullInt(by), fromMS(created), fromMS(updated)
	return w, nil
}

func countMemberships(ctx context.Context, tx *sql.Tx, userID int64) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM workspace_members WHERE user_id = ?", userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count workspaces: %w", err)
	}
	return n, nil
}

// CreateWorkspace inserts a workspace and its first member.
func (db *DB) CreateWorkspace(ctx context.Context, name string, creatorID int64, maxPerUser int, now time.Time) (int64, error) {
	var id int64
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		n, err := countMemberships(ctx, tx, creatorID)
		if err != nil {
			return err
		}
		if n >= maxPerUser {
			return domain.ErrWorkspaceLimit.WithParams(map[string]any{"limit": maxPerUser})
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO workspaces (name, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?)`, name, creatorID, toMS(now), toMS(now))
		if err != nil {
			return fmt.Errorf("insert workspace: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("insert workspace: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, added_by, created_at)
			VALUES (?, ?, ?, ?)`, id, creatorID, creatorID, toMS(now)); err != nil {
			return fmt.Errorf("insert workspace member: %w", err)
		}
		return nil
	})
	return id, err
}

// GetWorkspace loads one workspace.
func (db *DB) GetWorkspace(ctx context.Context, id int64) (domain.Workspace, error) {
	w, err := scanWorkspace(db.r.QueryRowContext(ctx, "SELECT "+workspaceCols+" FROM workspaces w WHERE w.id = ?", id))
	if err != nil {
		return domain.Workspace{}, mapNoRows(err, "get workspace")
	}
	return w, nil
}

// ListWorkspaces lists the workspaces of a member, or all of them.
func (db *DB) ListWorkspaces(ctx context.Context, memberID *int64) ([]domain.Workspace, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+workspaceCols+` FROM workspaces w
		WHERE ?1 IS NULL OR w.id IN (SELECT workspace_id FROM workspace_members WHERE user_id = ?1)
		ORDER BY w.name COLLATE NOCASE, w.id`, memberID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	out := []domain.Workspace{}
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	return out, nil
}

// RenameWorkspace changes the name of a workspace.
func (db *DB) RenameWorkspace(ctx context.Context, id int64, name string, now time.Time) error {
	res, err := db.w.ExecContext(ctx, "UPDATE workspaces SET name = ?, updated_at = ? WHERE id = ?", name, toMS(now), id)
	return expectAffected(res, err, "rename workspace")
}

func activeWorkspaceSites(ctx context.Context, tx *sql.Tx, id int64) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sites WHERE workspace_id = ? AND deleted_at IS NULL",
		id).Scan(&n); err != nil {
		return 0, fmt.Errorf("count workspace sites: %w", err)
	}
	return n, nil
}

// deleteWorkspaceTx deletes an empty workspace inside tx.
func deleteWorkspaceTx(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, "UPDATE sites SET workspace_id = NULL WHERE workspace_id = ?", id); err != nil {
		return fmt.Errorf("detach deleted sites: %w", err)
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM workspaces WHERE id = ?", id)
	return expectAffected(res, err, "delete workspace")
}

// DeleteWorkspace deletes a workspace without active sites.
func (db *DB) DeleteWorkspace(ctx context.Context, id int64) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		n, err := activeWorkspaceSites(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return domain.ErrWorkspaceNotEmpty
		}
		return deleteWorkspaceTx(ctx, tx, id)
	})
}

// IsWorkspaceMember reports whether userID belongs to the workspace.
func (db *DB) IsWorkspaceMember(ctx context.Context, workspaceID, userID int64) (bool, error) {
	var ok bool
	if err := db.r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspace_members
		WHERE workspace_id = ? AND user_id = ?)`, workspaceID, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check workspace member: %w", err)
	}
	return ok, nil
}

// ListWorkspaceMembers lists the members of a workspace.
func (db *DB) ListWorkspaceMembers(ctx context.Context, workspaceID int64) ([]domain.WorkspaceMember, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT m.user_id, u.email, m.created_at FROM workspace_members m
		JOIN users u ON u.id = m.user_id WHERE m.workspace_id = ? ORDER BY u.email, m.user_id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace members: %w", err)
	}
	defer rows.Close()
	out := []domain.WorkspaceMember{}
	for rows.Next() {
		var m domain.WorkspaceMember
		var at int64
		if err := rows.Scan(&m.UserID, &m.Email, &at); err != nil {
			return nil, fmt.Errorf("scan workspace member: %w", err)
		}
		m.AddedAt = fromMS(at)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workspace members: %w", err)
	}
	return out, nil
}

// AddWorkspaceMember adds a member within the limits.
func (db *DB) AddWorkspaceMember(ctx context.Context, workspaceID, userID, addedBy int64, maxMembers, maxPerUser int, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		var members int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM workspace_members WHERE workspace_id = ?",
			workspaceID).Scan(&members); err != nil {
			return fmt.Errorf("count workspace members: %w", err)
		}
		if members == 0 {
			return domain.ErrNotFound // deleted meanwhile: a workspace always has members
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspace_members
			WHERE workspace_id = ? AND user_id = ?)`, workspaceID, userID).Scan(&exists); err != nil {
			return fmt.Errorf("check workspace member: %w", err)
		}
		if exists {
			return domain.ErrWorkspaceMemberExists
		}
		if members >= maxMembers {
			return domain.ErrWorkspaceLimit.WithParams(map[string]any{"limit": maxMembers})
		}
		n, err := countMemberships(ctx, tx, userID)
		if err != nil {
			return err
		}
		if n >= maxPerUser {
			return domain.ErrWorkspaceLimit.WithParams(map[string]any{"limit": maxPerUser})
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, added_by, created_at)
			VALUES (?, ?, ?, ?)`, workspaceID, userID, addedBy, toMS(now)); err != nil {
			return fmt.Errorf("insert workspace member: %w", err)
		}
		return nil
	})
}

// RemoveWorkspaceMember removes a member; see the interface for the last one.
func (db *DB) RemoveWorkspaceMember(ctx context.Context, workspaceID, userID int64) (bool, error) {
	deleted := false
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?",
			workspaceID, userID)
		if err := expectAffected(res, err, "remove workspace member"); err != nil {
			return err
		}
		var left int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM workspace_members WHERE workspace_id = ?",
			workspaceID).Scan(&left); err != nil {
			return fmt.Errorf("count workspace members: %w", err)
		}
		if left > 0 {
			return nil
		}
		sites, err := activeWorkspaceSites(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		if sites > 0 {
			return domain.ErrWorkspaceLastMember
		}
		deleted = true
		return deleteWorkspaceTx(ctx, tx, workspaceID)
	})
	return deleted, err
}

// MoveSite changes the workspace of an active site.
func (db *DB) MoveSite(ctx context.Context, siteID string, workspaceID *int64, memberID *int64, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if workspaceID != nil {
			q := "SELECT EXISTS (SELECT 1 FROM workspaces WHERE id = ?1)"
			args := []any{*workspaceID}
			if memberID != nil {
				q = "SELECT EXISTS (SELECT 1 FROM workspace_members WHERE workspace_id = ?1 AND user_id = ?2)"
				args = append(args, *memberID)
			}
			var ok bool
			if err := tx.QueryRowContext(ctx, q, args...).Scan(&ok); err != nil {
				return fmt.Errorf("check target workspace: %w", err)
			}
			if !ok {
				return domain.ErrNotFound
			}
		}
		res, err := tx.ExecContext(ctx, "UPDATE sites SET workspace_id = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
			nullInt(workspaceID), toMS(now), siteID)
		return expectAffected(res, err, "move site")
	})
}

// SiteWorkspaceID returns the workspace of a site, if any.
func (db *DB) SiteWorkspaceID(ctx context.Context, siteID string) (*int64, error) {
	var ws sql.NullInt64
	err := db.r.QueryRowContext(ctx, "SELECT workspace_id FROM sites WHERE id = ?", siteID).Scan(&ws)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("site workspace: %w", err)
	}
	return fromNullInt(ws), nil
}
