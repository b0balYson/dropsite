package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ProxyRepository stores the secret proxy endpoints of sites.
type ProxyRepository interface {
	CountProxyEndpoints(ctx context.Context, siteID string) (int, error)
	ListProxyEndpoints(ctx context.Context, siteID string) ([]domain.ProxyEndpoint, error)
	// GetProxyEndpoint returns the endpoint only if it belongs to siteID.
	GetProxyEndpoint(ctx context.Context, siteID, name string) (domain.ProxyEndpoint, error)
	// CreateProxyEndpoint inserts e; a taken name returns a conflict.
	CreateProxyEndpoint(ctx context.Context, e domain.ProxyEndpoint) (int64, error)
	// UpdateProxyEndpoint stores the configuration, the sealed secret and
	// the timestamps of e (matched by ID and SiteID); usage is kept.
	UpdateProxyEndpoint(ctx context.Context, e domain.ProxyEndpoint) error
	DeleteProxyEndpoint(ctx context.Context, siteID string, id int64) error
	// RecordProxyUse counts one call of an endpoint.
	RecordProxyUse(ctx context.Context, id int64, outcome ProxyOutcome, status int, now time.Time) error
	// AddProxyRejected adds n calls refused before forwarding in one write.
	AddProxyRejected(ctx context.Context, id int64, n, status int, at time.Time) error
	// PurgeProxyEndpoints deletes the endpoints (and secrets) of deleted sites.
	PurgeProxyEndpoints(ctx context.Context) (int, error)
}

// ProxyOutcome classifies a call for the usage counters.
type ProxyOutcome int

const (
	// ProxyForwarded: the upstream answered below 500.
	ProxyForwarded ProxyOutcome = iota
	// ProxyFailed: the upstream answered 5xx or could not be reached.
	ProxyFailed
	// ProxyRejected: the call was refused before anything was forwarded.
	ProxyRejected
)

var errProxyTaken = domain.E(domain.KindConflict, domain.CodeProxyInvalid,
	"an endpoint with this name already exists; try again")

const proxyCols = `id, site_id, name, upstream_url, allowed_hosts, methods, paths, auth_kind, auth_name, enabled,
	secret_enc, secret_updated_at, requests, errors, rejected, last_used_at, last_status, created_at, updated_at`

func encodeList(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode list: %w", err)
	}
	return string(b), nil
}

func decodeList(s string) ([]string, error) {
	out := []string{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	return out, nil
}

func scanProxy(r rowScanner) (domain.ProxyEndpoint, error) {
	var (
		e                       domain.ProxyEndpoint
		hosts, methods, paths   string
		auth                    string
		secretAt, lastUsed      sql.NullInt64
		createdAt, updatedAt    int64
		requests, errs, refused int64
	)
	if err := r.Scan(&e.ID, &e.SiteID, &e.Name, &e.UpstreamURL, &hosts, &methods, &paths, &auth, &e.AuthName,
		&e.Enabled, &e.SecretEnc, &secretAt, &requests, &errs, &refused, &lastUsed, &e.Usage.LastStatus,
		&createdAt, &updatedAt); err != nil {
		return domain.ProxyEndpoint{}, err
	}
	var err error
	if e.AllowedHosts, err = decodeList(hosts); err != nil {
		return domain.ProxyEndpoint{}, err
	}
	if e.Methods, err = decodeList(methods); err != nil {
		return domain.ProxyEndpoint{}, err
	}
	if e.Paths, err = decodeList(paths); err != nil {
		return domain.ProxyEndpoint{}, err
	}
	e.Auth = domain.ProxyAuth(auth)
	e.SecretUpdatedAt = fromNullMS(secretAt)
	e.Usage.Requests, e.Usage.Errors, e.Usage.Rejected = requests, errs, refused
	e.Usage.LastUsedAt = fromNullMS(lastUsed)
	e.CreatedAt, e.UpdatedAt = fromMS(createdAt), fromMS(updatedAt)
	return e, nil
}

// CountProxyEndpoints implements ProxyRepository.
func (db *DB) CountProxyEndpoints(ctx context.Context, siteID string) (int, error) {
	var n int
	if err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM proxy_endpoints WHERE site_id = ?", siteID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count proxy endpoints: %w", err)
	}
	return n, nil
}

// ListProxyEndpoints implements ProxyRepository.
func (db *DB) ListProxyEndpoints(ctx context.Context, siteID string) ([]domain.ProxyEndpoint, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+proxyCols+" FROM proxy_endpoints WHERE site_id = ? ORDER BY name", siteID)
	if err != nil {
		return nil, fmt.Errorf("list proxy endpoints: %w", err)
	}
	defer rows.Close()
	out := []domain.ProxyEndpoint{}
	for rows.Next() {
		e, err := scanProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proxy endpoint: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list proxy endpoints: %w", err)
	}
	return out, nil
}

// GetProxyEndpoint implements ProxyRepository.
func (db *DB) GetProxyEndpoint(ctx context.Context, siteID, name string) (domain.ProxyEndpoint, error) {
	row := db.r.QueryRowContext(ctx, "SELECT "+proxyCols+" FROM proxy_endpoints WHERE site_id = ? AND name = ?", siteID, name)
	e, err := scanProxy(row)
	if err != nil {
		return domain.ProxyEndpoint{}, mapNoRows(err, "get proxy endpoint")
	}
	return e, nil
}

type proxyLists struct{ hosts, methods, paths string }

func encodeProxyLists(c domain.ProxyConfig) (proxyLists, error) {
	var l proxyLists
	var err error
	if l.hosts, err = encodeList(c.AllowedHosts); err != nil {
		return l, err
	}
	if l.methods, err = encodeList(c.Methods); err != nil {
		return l, err
	}
	l.paths, err = encodeList(c.Paths)
	return l, err
}

// CreateProxyEndpoint implements ProxyRepository.
func (db *DB) CreateProxyEndpoint(ctx context.Context, e domain.ProxyEndpoint) (int64, error) {
	l, err := encodeProxyLists(e.ProxyConfig)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.withTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveSite(ctx, tx, e.SiteID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO proxy_endpoints (site_id, name, upstream_url, allowed_hosts, methods,
			paths, auth_kind, auth_name, enabled, secret_enc, secret_updated_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.SiteID, e.Name, e.UpstreamURL, l.hosts, l.methods, l.paths, string(e.Auth), e.AuthName, e.Enabled,
			e.SecretEnc, nullMS(e.SecretUpdatedAt), toMS(e.CreatedAt), toMS(e.UpdatedAt))
		if err != nil {
			return mapUnique(err, errProxyTaken, "create proxy endpoint")
		}
		id, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("create proxy endpoint: last id: %w", err)
		}
		return nil
	})
	return id, err
}

// UpdateProxyEndpoint implements ProxyRepository.
func (db *DB) UpdateProxyEndpoint(ctx context.Context, e domain.ProxyEndpoint) error {
	l, err := encodeProxyLists(e.ProxyConfig)
	if err != nil {
		return err
	}
	res, err := db.w.ExecContext(ctx, `UPDATE proxy_endpoints SET upstream_url = ?, allowed_hosts = ?, methods = ?,
		paths = ?, auth_kind = ?, auth_name = ?, enabled = ?, secret_enc = ?, secret_updated_at = ?, updated_at = ?
		WHERE id = ? AND site_id = ?`,
		e.UpstreamURL, l.hosts, l.methods, l.paths, string(e.Auth), e.AuthName, e.Enabled, e.SecretEnc,
		nullMS(e.SecretUpdatedAt), toMS(e.UpdatedAt), e.ID, e.SiteID)
	return expectAffected(res, err, "update proxy endpoint")
}

// DeleteProxyEndpoint implements ProxyRepository.
func (db *DB) DeleteProxyEndpoint(ctx context.Context, siteID string, id int64) error {
	res, err := db.w.ExecContext(ctx, "DELETE FROM proxy_endpoints WHERE id = ? AND site_id = ?", id, siteID)
	return expectAffected(res, err, "delete proxy endpoint")
}

// RecordProxyUse implements ProxyRepository.
func (db *DB) RecordProxyUse(ctx context.Context, id int64, outcome ProxyOutcome, status int, now time.Time) error {
	var col string
	switch outcome {
	case ProxyForwarded:
		col = "requests = requests + 1"
	case ProxyFailed:
		col = "requests = requests + 1, errors = errors + 1"
	default:
		col = "rejected = rejected + 1"
	}
	// col is one of the constant fragments above.
	_, err := db.w.ExecContext(ctx, "UPDATE proxy_endpoints SET "+col+", last_used_at = ?, last_status = ? WHERE id = ?", //nolint:gosec // constant fragments
		toMS(now), status, id)
	if err != nil {
		return fmt.Errorf("record proxy use: %w", err)
	}
	return nil
}

// PurgeProxyEndpoints implements ProxyRepository.
func (db *DB) PurgeProxyEndpoints(ctx context.Context) (int, error) {
	res, err := db.w.ExecContext(ctx,
		"DELETE FROM proxy_endpoints WHERE site_id IN (SELECT id FROM sites WHERE deleted_at IS NOT NULL)")
	return affected(res, err, "purge proxy endpoints")
}
