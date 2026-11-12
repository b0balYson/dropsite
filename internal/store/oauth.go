package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// ErrCredentialReused reports a second use of a one-time OAuth credential (an
// authorization code or a refresh token). The grant it belongs to has been
// revoked by the call that returns it.
var ErrCredentialReused = errors.New("oauth credential reused")

// OAuthRepository stores the OAuth authorization server state.
type OAuthRepository interface {
	CreateOAuthClient(ctx context.Context, c domain.OAuthClient) error
	GetOAuthClient(ctx context.Context, id string) (domain.OAuthClient, error)
	CountOAuthClients(ctx context.Context) (int, error)
	// DeleteUnusedOAuthClients removes clients registered before the given
	// time that have no active grant.
	DeleteUnusedOAuthClients(ctx context.Context, before time.Time) (int, error)

	CreateOAuthCode(ctx context.Context, c domain.OAuthCode) (int64, error)
	// RedeemOAuthCode marks a usable code used and returns it. An unknown or
	// expired code → ErrNotFound; a used one revokes the grant issued for it
	// and returns ErrCredentialReused.
	RedeemOAuthCode(ctx context.Context, hash []byte, now time.Time) (domain.OAuthCode, error)
	// IssueOAuthGrant creates a grant for a redeemed code, revokes earlier
	// active grants of the same user and client, links the code to the grant
	// and stores the tokens, atomically. It returns the grant ID.
	IssueOAuthGrant(ctx context.Context, codeID int64, g domain.OAuthGrant, tokens []domain.OAuthToken) (int64, error)
	// GetOAuthToken loads a token and its grant by the token hash.
	GetOAuthToken(ctx context.Context, hash []byte) (domain.OAuthToken, domain.OAuthGrant, error)
	// RotateOAuthRefresh marks the refresh token used and stores the new
	// tokens. If it was already used, revoked or expired, the whole grant is
	// revoked and ErrCredentialReused is returned.
	RotateOAuthRefresh(ctx context.Context, oldID, grantID int64, now time.Time, tokens []domain.OAuthToken) error
	RevokeOAuthGrant(ctx context.Context, grantID int64, now time.Time) error
	// RevokeUserOAuthGrant revokes the user's grant; a foreign or missing grant → ErrNotFound.
	RevokeUserOAuthGrant(ctx context.Context, userID, grantID int64, now time.Time) (domain.OAuthGrant, error)
	// ListOAuthGrants returns the user's active grants, newest first.
	ListOAuthGrants(ctx context.Context, userID int64) ([]domain.OAuthGrant, error)
	TouchOAuthGrant(ctx context.Context, grantID int64, now time.Time) error
	// DeleteExpiredOAuth removes expired codes and tokens and tokens of revoked grants.
	DeleteExpiredOAuth(ctx context.Context, now time.Time) (int, error)
}

// CreateOAuthClient stores a registered client.
func (db *DB) CreateOAuthClient(ctx context.Context, c domain.OAuthClient) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return fmt.Errorf("encode redirect uris: %w", err)
	}
	_, err = db.w.ExecContext(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris, created_at, created_ip)
		VALUES (?, ?, ?, ?, ?)`, c.ID, c.Name, string(uris), toMS(c.CreatedAt), c.CreatedIP)
	if err != nil {
		return fmt.Errorf("insert oauth client: %w", err)
	}
	return nil
}

// GetOAuthClient loads a registered client.
func (db *DB) GetOAuthClient(ctx context.Context, id string) (domain.OAuthClient, error) {
	var c domain.OAuthClient
	var uris string
	var created int64
	err := db.r.QueryRowContext(ctx, `SELECT id, name, redirect_uris, created_at, created_ip
		FROM oauth_clients WHERE id = ?`, id).Scan(&c.ID, &c.Name, &uris, &created, &c.CreatedIP)
	if err != nil {
		return domain.OAuthClient{}, mapNoRows(err, "get oauth client")
	}
	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		return domain.OAuthClient{}, fmt.Errorf("decode redirect uris: %w", err)
	}
	c.CreatedAt = fromMS(created)
	return c, nil
}

// CountOAuthClients returns the number of registered clients.
func (db *DB) CountOAuthClients(ctx context.Context) (int, error) {
	var n int
	if err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM oauth_clients").Scan(&n); err != nil {
		return 0, fmt.Errorf("count oauth clients: %w", err)
	}
	return n, nil
}

// DeleteUnusedOAuthClients drops stale registrations without active grants.
func (db *DB) DeleteUnusedOAuthClients(ctx context.Context, before time.Time) (int, error) {
	res, err := db.w.ExecContext(ctx, `DELETE FROM oauth_clients WHERE created_at < ?
		AND id NOT IN (SELECT client_id FROM oauth_grants WHERE revoked_at IS NULL)`, toMS(before))
	return affected(res, err, "delete unused oauth clients")
}

// CreateOAuthCode stores an authorization code.
func (db *DB) CreateOAuthCode(ctx context.Context, c domain.OAuthCode) (int64, error) {
	res, err := db.w.ExecContext(ctx, `INSERT INTO oauth_codes
		(hash, user_id, client_id, client_name, redirect_uri, challenge, resource, scope, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Hash, c.UserID, c.ClientID, c.ClientName, c.RedirectURI, c.Challenge, c.Resource, c.Scope,
		toMS(c.CreatedAt), toMS(c.ExpiresAt))
	if err != nil {
		return 0, fmt.Errorf("insert oauth code: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert oauth code: last id: %w", err)
	}
	return id, nil
}

const oauthCodeCols = "id, hash, user_id, client_id, client_name, redirect_uri, challenge, resource, scope, " +
	"created_at, expires_at, used_at, grant_id"

func scanOAuthCode(s rowScanner) (domain.OAuthCode, error) {
	var c domain.OAuthCode
	var created, expires int64
	var used, grant sql.NullInt64
	if err := s.Scan(&c.ID, &c.Hash, &c.UserID, &c.ClientID, &c.ClientName, &c.RedirectURI, &c.Challenge,
		&c.Resource, &c.Scope, &created, &expires, &used, &grant); err != nil {
		return domain.OAuthCode{}, err
	}
	c.CreatedAt, c.ExpiresAt, c.UsedAt = fromMS(created), fromMS(expires), fromNullMS(used)
	if grant.Valid {
		g := grant.Int64
		c.GrantID = &g
	}
	return c, nil
}

// RedeemOAuthCode consumes an authorization code (see the interface).
func (db *DB) RedeemOAuthCode(ctx context.Context, hash []byte, now time.Time) (domain.OAuthCode, error) {
	var c domain.OAuthCode
	reused := false
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		c, err = scanOAuthCode(tx.QueryRowContext(ctx, "SELECT "+oauthCodeCols+" FROM oauth_codes WHERE hash = ?", hash))
		if err != nil {
			return mapNoRows(err, "get oauth code")
		}
		if c.UsedAt != nil {
			reused = true
			if c.GrantID != nil {
				return revokeGrant(ctx, tx, *c.GrantID, now)
			}
			return nil
		}
		if !now.Before(c.ExpiresAt) {
			return domain.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, "UPDATE oauth_codes SET used_at = ? WHERE id = ?", toMS(now), c.ID); err != nil {
			return fmt.Errorf("mark oauth code used: %w", err)
		}
		used := now
		c.UsedAt = &used
		return nil
	})
	if err != nil {
		return domain.OAuthCode{}, err
	}
	if reused {
		return domain.OAuthCode{}, ErrCredentialReused
	}
	return c, nil
}

func revokeGrant(ctx context.Context, tx *sql.Tx, grantID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, "UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?",
		toMS(now), grantID); err != nil {
		return fmt.Errorf("revoke oauth grant: %w", err)
	}
	return nil
}

func insertOAuthTokens(ctx context.Context, tx *sql.Tx, grantID int64, tokens []domain.OAuthToken) error {
	for _, t := range tokens {
		if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens (hash, kind, grant_id, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?)`, t.Hash, t.Kind, grantID, toMS(t.CreatedAt), toMS(t.ExpiresAt)); err != nil {
			return fmt.Errorf("insert oauth token: %w", err)
		}
	}
	return nil
}

// IssueOAuthGrant creates the grant for a redeemed code (see the interface).
func (db *DB) IssueOAuthGrant(ctx context.Context, codeID int64, g domain.OAuthGrant, tokens []domain.OAuthToken) (int64, error) {
	var id int64
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at = ?
			WHERE user_id = ? AND client_id = ? AND revoked_at IS NULL`,
			toMS(g.CreatedAt), g.UserID, g.ClientID); err != nil {
			return fmt.Errorf("revoke previous grants: %w", err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO oauth_grants
			(user_id, client_id, client_name, redirect_host, scope, resource, created_at, last_used_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			g.UserID, g.ClientID, g.ClientName, g.RedirectHost, g.Scope, g.Resource, toMS(g.CreatedAt), toMS(g.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert oauth grant: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("insert oauth grant: last id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE oauth_codes SET grant_id = ? WHERE id = ?", id, codeID); err != nil {
			return fmt.Errorf("link oauth code: %w", err)
		}
		return insertOAuthTokens(ctx, tx, id, tokens)
	})
	return id, err
}

const oauthGrantCols = "g.id, g.user_id, g.client_id, g.client_name, g.redirect_host, g.scope, g.resource, " +
	"g.created_at, g.last_used_at, g.revoked_at"

type grantRow struct {
	g             domain.OAuthGrant
	created       int64
	used, revoked sql.NullInt64
}

func (r *grantRow) dest() []any {
	return []any{&r.g.ID, &r.g.UserID, &r.g.ClientID, &r.g.ClientName, &r.g.RedirectHost, &r.g.Scope,
		&r.g.Resource, &r.created, &r.used, &r.revoked}
}

func (r *grantRow) grant() domain.OAuthGrant {
	g := r.g
	g.CreatedAt, g.LastUsedAt, g.RevokedAt = fromMS(r.created), fromNullMS(r.used), fromNullMS(r.revoked)
	return g
}

// GetOAuthToken loads a token with its grant.
func (db *DB) GetOAuthToken(ctx context.Context, hash []byte) (domain.OAuthToken, domain.OAuthGrant, error) {
	var t domain.OAuthToken
	var created, expires int64
	var used, revoked sql.NullInt64
	var gr grantRow
	dest := append([]any{&t.ID, &t.Hash, &t.Kind, &t.GrantID, &created, &expires, &used, &revoked}, gr.dest()...)
	err := db.r.QueryRowContext(ctx, `SELECT t.id, t.hash, t.kind, t.grant_id, t.created_at, t.expires_at,
		t.used_at, t.revoked_at, `+oauthGrantCols+`
		FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id WHERE t.hash = ?`, hash).Scan(dest...)
	if err != nil {
		return domain.OAuthToken{}, domain.OAuthGrant{}, mapNoRows(err, "get oauth token")
	}
	t.CreatedAt, t.ExpiresAt = fromMS(created), fromMS(expires)
	t.UsedAt, t.RevokedAt = fromNullMS(used), fromNullMS(revoked)
	return t, gr.grant(), nil
}

// RotateOAuthRefresh replaces a refresh token (see the interface).
func (db *DB) RotateOAuthRefresh(ctx context.Context, oldID, grantID int64, now time.Time, tokens []domain.OAuthToken) error {
	reused := false
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE oauth_tokens SET used_at = ?
			WHERE id = ? AND grant_id = ? AND kind = 'refresh' AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?
			AND grant_id IN (SELECT id FROM oauth_grants WHERE revoked_at IS NULL)`,
			toMS(now), oldID, grantID, toMS(now))
		n, err := affected(res, err, "mark refresh token used")
		if err != nil {
			return err
		}
		if n == 0 {
			reused = true
			return revokeGrant(ctx, tx, grantID, now)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE oauth_grants SET last_used_at = ? WHERE id = ?", toMS(now), grantID); err != nil {
			return fmt.Errorf("touch oauth grant: %w", err)
		}
		return insertOAuthTokens(ctx, tx, grantID, tokens)
	})
	if err != nil {
		return err
	}
	if reused {
		return ErrCredentialReused
	}
	return nil
}

// RevokeOAuthGrant revokes a grant by ID (protocol-level revocation).
func (db *DB) RevokeOAuthGrant(ctx context.Context, grantID int64, now time.Time) error {
	return db.withTx(ctx, func(tx *sql.Tx) error { return revokeGrant(ctx, tx, grantID, now) })
}

// RevokeUserOAuthGrant revokes one of the user's active grants.
func (db *DB) RevokeUserOAuthGrant(ctx context.Context, userID, grantID int64, now time.Time) (domain.OAuthGrant, error) {
	var gr grantRow
	err := db.w.QueryRowContext(ctx, `UPDATE oauth_grants SET revoked_at = ?
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL RETURNING `+strings.ReplaceAll(oauthGrantCols, "g.", ""),
		toMS(now), grantID, userID).Scan(gr.dest()...)
	if err != nil {
		return domain.OAuthGrant{}, mapNoRows(err, "revoke oauth grant")
	}
	return gr.grant(), nil
}

// ListOAuthGrants returns the user's active grants.
func (db *DB) ListOAuthGrants(ctx context.Context, userID int64) ([]domain.OAuthGrant, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT "+oauthGrantCols+
		" FROM oauth_grants g WHERE g.user_id = ? AND g.revoked_at IS NULL ORDER BY g.created_at DESC, g.id DESC", userID)
	if err != nil {
		return nil, fmt.Errorf("list oauth grants: %w", err)
	}
	defer rows.Close()
	out := []domain.OAuthGrant{}
	for rows.Next() {
		var gr grantRow
		if err := rows.Scan(gr.dest()...); err != nil {
			return nil, fmt.Errorf("scan oauth grant: %w", err)
		}
		out = append(out, gr.grant())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list oauth grants: %w", err)
	}
	return out, nil
}

// TouchOAuthGrant records the last use of a grant.
func (db *DB) TouchOAuthGrant(ctx context.Context, grantID int64, now time.Time) error {
	if _, err := db.w.ExecContext(ctx, "UPDATE oauth_grants SET last_used_at = ? WHERE id = ?", toMS(now), grantID); err != nil {
		return fmt.Errorf("touch oauth grant: %w", err)
	}
	return nil
}

// DeleteExpiredOAuth removes dead codes and tokens.
func (db *DB) DeleteExpiredOAuth(ctx context.Context, now time.Time) (int, error) {
	total := 0
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			"DELETE FROM oauth_tokens WHERE expires_at < ?",
			"DELETE FROM oauth_tokens WHERE grant_id IN (SELECT id FROM oauth_grants WHERE revoked_at IS NOT NULL AND revoked_at < ?)",
			"DELETE FROM oauth_codes WHERE expires_at < ?",
		} {
			res, err := tx.ExecContext(ctx, q, toMS(now))
			n, err := affected(res, err, "delete expired oauth state")
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	return total, err
}
