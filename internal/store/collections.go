package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// CollectionRepository stores site collections and their documents. Every
// method ignores collections of deleted sites only through the explicit
// deletion in SoftDeleteSite; callers resolve the site first.
type CollectionRepository interface {
	// ListCollections returns the site's collections by name, with counts.
	ListCollections(ctx context.Context, siteID string) ([]domain.Collection, error)
	// GetCollection returns one collection with counts.
	GetCollection(ctx context.Context, siteID, name string) (domain.Collection, error)
	// CreateCollection inserts c unless the site has maxPerSite collections
	// (CodeLimitExceeded) or one with the name (CodeCollectionExists).
	CreateCollection(ctx context.Context, c domain.Collection, maxPerSite int) error
	// UpdateCollection stores title, mode and schema of c.
	UpdateCollection(ctx context.Context, c domain.Collection) error
	// DeleteCollection removes the collection with its documents.
	DeleteCollection(ctx context.Context, siteID, name string) error

	// SaveDocuments writes docs as approved documents atomically; replace
	// first removes every document of the collection. More than maxDocs
	// documents afterwards → CodeLimitExceeded and nothing is written.
	SaveDocuments(ctx context.Context, w DocumentsWrite) error
	// InsertPendingDocument stores a visitor proposal unless the collection
	// holds maxPending pending (CodeCollectionPendingFull) or maxDocs
	// documents (CodeLimitExceeded).
	InsertPendingDocument(ctx context.Context, d domain.Document, maxDocs, maxPending int) error
	// DeleteDocuments removes the listed documents and returns how many
	// existed.
	DeleteDocuments(ctx context.Context, siteID, collection string, ids []string, now time.Time) (int, error)
	// ModerateDocuments approves or deletes (reject) the listed pending
	// documents and returns how many were pending.
	ModerateDocuments(ctx context.Context, siteID, collection string, ids []string, approve bool, now time.Time) (int, error)
	// GetDocument returns one document of the collection.
	GetDocument(ctx context.Context, siteID, collection, id string) (domain.Document, error)
	// ListDocuments returns up to f.Limit documents and whether more follow.
	ListDocuments(ctx context.Context, f DocumentFilter) ([]domain.Document, bool, error)
}

// DocumentsWrite is one owner write of SaveDocuments.
type DocumentsWrite struct {
	SiteID     string
	Collection string
	Docs       []DocumentInput
	Replace    bool
	MaxDocs    int
	Now        time.Time
}

// DocumentInput is one written document; a nil Position keeps the stored
// position of an existing document (0 for a new one).
type DocumentInput struct {
	ID       string
	Data     json.RawMessage
	Position *int64
}

// Document orders.
const (
	OrderPosition = "position"
	OrderNewest   = "newest"
	OrderOldest   = "oldest"
)

// DocumentFilter selects documents. Status "" means any status; After
// continues after a document in the same order.
type DocumentFilter struct {
	SiteID     string
	Collection string
	Status     domain.DocumentStatus
	Order      string
	After      *DocumentCursor
	Limit      int
}

// DocumentCursor is the position of a document in a listing.
type DocumentCursor struct {
	Position  int64
	CreatedAt time.Time
	ID        string
}

const collectionCols = `c.site_id, c.name, c.title, c.mode, c.schema_json, c.data_version, c.created_at, c.updated_at,
	(SELECT COUNT(*) FROM site_documents d WHERE d.site_id = c.site_id AND d.collection = c.name),
	(SELECT COUNT(*) FROM site_documents d WHERE d.site_id = c.site_id AND d.collection = c.name AND d.status = 'pending')`

func scanCollection(s rowScanner) (domain.Collection, error) {
	var c domain.Collection
	var schema sql.NullString
	var created, updated int64
	if err := s.Scan(&c.SiteID, &c.Name, &c.Title, &c.Mode, &schema, &c.DataVersion, &created, &updated,
		&c.Documents, &c.Pending); err != nil {
		return domain.Collection{}, err
	}
	if schema.Valid {
		c.Schema = json.RawMessage(schema.String)
	}
	c.CreatedAt, c.UpdatedAt = fromMS(created), fromMS(updated)
	return c, nil
}

func nullSchema(s json.RawMessage) any {
	if len(s) == 0 {
		return nil
	}
	return string(s)
}

// ListCollections returns the site's collections ordered by name.
func (db *DB) ListCollections(ctx context.Context, siteID string) ([]domain.Collection, error) {
	rows, err := db.r.QueryContext(ctx, `SELECT `+collectionCols+` FROM site_collections c
		WHERE c.site_id = ? ORDER BY c.name`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, fmt.Errorf("list collections: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	return out, nil
}

// GetCollection returns one collection with counts.
func (db *DB) GetCollection(ctx context.Context, siteID, name string) (domain.Collection, error) {
	c, err := scanCollection(db.r.QueryRowContext(ctx, `SELECT `+collectionCols+` FROM site_collections c
		WHERE c.site_id = ? AND c.name = ?`, siteID, name))
	if err != nil {
		return domain.Collection{}, mapNoRows(err, "get collection")
	}
	return c, nil
}

func limitExceeded(what string, limit int) error {
	return domain.E(domain.KindLimit, domain.CodeLimitExceeded, fmt.Sprintf("at most %d %s", limit, what)).
		WithParams(map[string]any{"limit": limit, "what": what})
}

var errCollectionExists = domain.E(domain.KindConflict, domain.CodeCollectionExists, "the site already has a collection with this name")

// CreateCollection inserts a collection within the per-site limit.
func (db *DB) CreateCollection(ctx context.Context, c domain.Collection, maxPerSite int) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, c.SiteID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM site_collections WHERE site_id = ?", c.SiteID).Scan(&n); err != nil {
			return fmt.Errorf("count collections: %w", err)
		}
		if n >= maxPerSite {
			return limitExceeded("collections per site", maxPerSite)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO site_collections (site_id, name, title, mode, schema_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, c.SiteID, c.Name, c.Title, string(c.Mode), nullSchema(c.Schema),
			toMS(c.CreatedAt), toMS(c.UpdatedAt))
		if err != nil {
			return mapUnique(err, errCollectionExists, "create collection")
		}
		return nil
	})
}

// UpdateCollection stores the mutable fields of c.
func (db *DB) UpdateCollection(ctx context.Context, c domain.Collection) error {
	res, err := db.w.ExecContext(ctx, `UPDATE site_collections SET title = ?, mode = ?, schema_json = ?, updated_at = ?
		WHERE site_id = ? AND name = ?`, c.Title, string(c.Mode), nullSchema(c.Schema), toMS(c.UpdatedAt), c.SiteID, c.Name)
	return expectAffected(res, err, "update collection")
}

// DeleteCollection removes a collection and its documents.
func (db *DB) DeleteCollection(ctx context.Context, siteID, name string) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM site_documents WHERE site_id = ? AND collection = ?", siteID, name); err != nil {
			return fmt.Errorf("delete documents: %w", err)
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM site_collections WHERE site_id = ? AND name = ?", siteID, name)
		return expectAffected(res, err, "delete collection")
	})
}

// deleteSiteCollections removes every collection of a site (site deletion).
func deleteSiteCollections(ctx context.Context, tx *sql.Tx, siteID string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM site_documents WHERE site_id = ?", siteID); err != nil {
		return fmt.Errorf("delete site documents: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM site_collections WHERE site_id = ?", siteID); err != nil {
		return fmt.Errorf("delete site collections: %w", err)
	}
	return nil
}

func requireCollection(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM site_collections WHERE site_id = ? AND name = ?", siteID, name).Scan(&one)
	if err != nil {
		return mapNoRows(err, "find collection")
	}
	return nil
}

func countDocuments(ctx context.Context, tx *sql.Tx, siteID, name, status string) (int, error) {
	q, args := "SELECT COUNT(*) FROM site_documents WHERE site_id = ? AND collection = ?", []any{siteID, name}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	var n int
	if err := tx.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return n, nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE site_collections SET data_version = data_version + 1 WHERE site_id = ? AND name = ?",
		siteID, name); err != nil {
		return fmt.Errorf("bump data version: %w", err)
	}
	return nil
}

// SaveDocuments writes owner documents atomically.
func (db *DB) SaveDocuments(ctx context.Context, w DocumentsWrite) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireCollection(ctx, tx, w.SiteID, w.Collection); err != nil {
			return err
		}
		if w.Replace {
			if _, err := tx.ExecContext(ctx, "DELETE FROM site_documents WHERE site_id = ? AND collection = ?",
				w.SiteID, w.Collection); err != nil {
				return fmt.Errorf("replace documents: %w", err)
			}
		}
		now := toMS(w.Now)
		for _, d := range w.Docs {
			var pos any
			if d.Position != nil {
				pos = *d.Position
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO site_documents
				(site_id, collection, id, data_json, position, status, created_at, updated_at)
				VALUES (?, ?, ?, ?, COALESCE(?, 0), 'approved', ?, ?)
				ON CONFLICT (site_id, collection, id) DO UPDATE SET data_json = excluded.data_json,
					position = CASE WHEN ? IS NULL THEN site_documents.position ELSE excluded.position END,
					status = 'approved', updated_at = excluded.updated_at`,
				w.SiteID, w.Collection, d.ID, string(d.Data), pos, now, now, pos); err != nil {
				return fmt.Errorf("write document: %w", err)
			}
		}
		n, err := countDocuments(ctx, tx, w.SiteID, w.Collection, "")
		if err != nil {
			return err
		}
		if n > w.MaxDocs {
			return limitExceeded("documents per collection", w.MaxDocs)
		}
		return bumpVersion(ctx, tx, w.SiteID, w.Collection)
	})
}

// InsertPendingDocument stores a visitor proposal within the limits.
func (db *DB) InsertPendingDocument(ctx context.Context, d domain.Document, maxDocs, maxPending int) error {
	return db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireCollection(ctx, tx, d.SiteID, d.Collection); err != nil {
			return err
		}
		pending, err := countDocuments(ctx, tx, d.SiteID, d.Collection, string(domain.DocumentPending))
		if err != nil {
			return err
		}
		if pending >= maxPending {
			return domain.ErrCollectionPendingFull
		}
		total, err := countDocuments(ctx, tx, d.SiteID, d.Collection, "")
		if err != nil {
			return err
		}
		if total >= maxDocs {
			return limitExceeded("documents per collection", maxDocs)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO site_documents
			(site_id, collection, id, data_json, position, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, 0, 'pending', ?, ?)`,
			d.SiteID, d.Collection, d.ID, string(d.Data), toMS(d.CreatedAt), toMS(d.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert pending document: %w", err)
		}
		return bumpVersion(ctx, tx, d.SiteID, d.Collection)
	})
}

func idList(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// changeDocuments runs one statement over the listed documents of an
// existing collection and bumps the data version when it changed any.
func (db *DB) changeDocuments(ctx context.Context, siteID, collection, stmt string, lead []any, ids []string) (int, error) {
	var n int64
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireCollection(ctx, tx, siteID, collection); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		in, args := idList(ids)
		res, err := tx.ExecContext(ctx, strings.Replace(stmt, "(?ids)", "("+in+")", 1),
			append(append(lead, siteID, collection), args...)...)
		if err != nil {
			return fmt.Errorf("change documents: %w", err)
		}
		if n, err = res.RowsAffected(); err != nil {
			return fmt.Errorf("change documents: %w", err)
		}
		if n == 0 {
			return nil
		}
		return bumpVersion(ctx, tx, siteID, collection)
	})
	return int(n), err
}

// DeleteDocuments removes documents by ID.
func (db *DB) DeleteDocuments(ctx context.Context, siteID, collection string, ids []string, _ time.Time) (int, error) {
	return db.changeDocuments(ctx, siteID, collection,
		"DELETE FROM site_documents WHERE site_id = ? AND collection = ? AND id IN (?ids)", nil, ids)
}

// ModerateDocuments approves or rejects (deletes) pending documents.
func (db *DB) ModerateDocuments(ctx context.Context, siteID, collection string, ids []string, approve bool, now time.Time) (int, error) {
	if approve {
		return db.changeDocuments(ctx, siteID, collection, `UPDATE site_documents SET status = 'approved', updated_at = ?
			WHERE site_id = ? AND collection = ? AND status = 'pending' AND id IN (?ids)`, []any{toMS(now)}, ids)
	}
	return db.changeDocuments(ctx, siteID, collection,
		"DELETE FROM site_documents WHERE site_id = ? AND collection = ? AND status = 'pending' AND id IN (?ids)", nil, ids)
}

const documentCols = "site_id, collection, id, data_json, position, status, created_at, updated_at"

func scanDocument(s rowScanner) (domain.Document, error) {
	var d domain.Document
	var data string
	var created, updated int64
	if err := s.Scan(&d.SiteID, &d.Collection, &d.ID, &data, &d.Position, &d.Status, &created, &updated); err != nil {
		return domain.Document{}, err
	}
	d.Data = json.RawMessage(data)
	d.CreatedAt, d.UpdatedAt = fromMS(created), fromMS(updated)
	return d, nil
}

// GetDocument returns one document.
func (db *DB) GetDocument(ctx context.Context, siteID, collection, id string) (domain.Document, error) {
	d, err := scanDocument(db.r.QueryRowContext(ctx, "SELECT "+documentCols+
		" FROM site_documents WHERE site_id = ? AND collection = ? AND id = ?", siteID, collection, id))
	if err != nil {
		return domain.Document{}, mapNoRows(err, "get document")
	}
	return d, nil
}

// orderSQL returns the ORDER BY clause and the keyset condition of an order.
func orderSQL(order string, after *DocumentCursor) (string, string, []any) {
	var orderBy, cond string
	var args []any
	switch order {
	case OrderNewest:
		orderBy = "created_at DESC, id DESC"
		if after != nil {
			cond, args = " AND (created_at, id) < (?, ?)", []any{toMS(after.CreatedAt), after.ID}
		}
	case OrderOldest:
		orderBy = "created_at, id"
		if after != nil {
			cond, args = " AND (created_at, id) > (?, ?)", []any{toMS(after.CreatedAt), after.ID}
		}
	default:
		orderBy = "position, created_at, id"
		if after != nil {
			cond, args = " AND (position, created_at, id) > (?, ?, ?)", []any{after.Position, toMS(after.CreatedAt), after.ID}
		}
	}
	return orderBy, cond, args
}

// ListDocuments returns one page of documents in the requested order.
func (db *DB) ListDocuments(ctx context.Context, f DocumentFilter) ([]domain.Document, bool, error) {
	q := "SELECT " + documentCols + " FROM site_documents WHERE site_id = ? AND collection = ?"
	args := []any{f.SiteID, f.Collection}
	if f.Status != "" {
		q += " AND status = ?"
		args = append(args, string(f.Status))
	}
	orderBy, cond, condArgs := orderSQL(f.Order, f.After)
	q += cond + " ORDER BY " + orderBy + " LIMIT ?"
	args = append(append(args, condArgs...), f.Limit+1)
	rows, err := db.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, false, fmt.Errorf("list documents: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list documents: %w", err)
	}
	if len(out) > f.Limit {
		return out[:f.Limit], true, nil
	}
	return out, false, nil
}
