package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/b0balYson/dropsite/internal/domain"
)

// siteListWhere filters active sites by owner (?1, NULL = all), by
// visibility to a user (?5, NULL = all: see visibleSite), by workspace (?6,
// NULL = any, 0 = personal sites only) and by a LIKE pattern (?2, empty =
// none) over title, owner email and any hostname. ?3 and ?4 are the page.
var siteListWhere = ` WHERE s.deleted_at IS NULL AND (?1 IS NULL OR s.owner_id = ?1)
	AND (?5 IS NULL OR ` + visibleSite("?5") + `)
	AND (?6 IS NULL OR (?6 = 0 AND s.workspace_id IS NULL) OR s.workspace_id = ?6)
	AND (?2 = '' OR s.title LIKE ?2 ESCAPE '\' OR u.email LIKE ?2 ESCAPE '\'
		OR EXISTS (SELECT 1 FROM hostnames h WHERE h.site_id = s.id AND h.hostname LIKE ?2 ESCAPE '\'))`

const siteListFrom = ` FROM sites s JOIN users u ON u.id = s.owner_id LEFT JOIN workspaces w ON w.id = s.workspace_id`

// ListSites returns a page of active sites, most recently updated first, and
// the total number of matches. Hostnames are loaded with one extra query.
func (db *DB) ListSites(ctx context.Context, f SiteFilter) ([]SiteSummary, int, error) {
	var out []SiteSummary
	var total int
	err := db.withReadTx(ctx, func(tx *sql.Tx) error {
		pattern := likePattern(f.Query)
		if err := tx.QueryRowContext(ctx, "SELECT count(*)"+siteListFrom+siteListWhere, f.args(pattern)...).
			Scan(&total); err != nil {
			return fmt.Errorf("count sites: %w", err)
		}
		var err error
		if out, err = listSitePage(ctx, tx, f, pattern); err != nil {
			return err
		}
		return attachHostnames(ctx, tx, out)
	})
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func listSitePage(ctx context.Context, tx *sql.Tx, f SiteFilter, pattern string) ([]SiteSummary, error) {
	limit, offset := f.bounds()
	args := f.args(pattern)
	args[2], args[3] = limit, offset
	rows, err := tx.QueryContext(ctx, "SELECT "+siteColsS+", u.email, COALESCE(w.name, ''), d.created_at, d.size_bytes"+
		siteListFrom+` LEFT JOIN deploys d ON d.id = s.current_deploy_id AND d.site_id = s.id`+siteListWhere+
		` ORDER BY s.updated_at DESC, s.id LIMIT ?3 OFFSET ?4`, args...)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()
	out := []SiteSummary{}
	for rows.Next() {
		s, err := scanSiteSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	return out, nil
}

func scanSiteSummary(rows *sql.Rows) (SiteSummary, error) {
	var s SiteSummary
	var raw siteScan
	var deployAt, size sql.NullInt64
	if err := rows.Scan(append(raw.dest(&s.Site), &s.OwnerEmail, &s.WorkspaceName, &deployAt, &size)...); err != nil {
		return SiteSummary{}, err
	}
	raw.finish(&s.Site)
	s.LastDeployAt, s.SizeBytes = fromNullMS(deployAt), size.Int64
	s.Hostnames = []domain.Hostname{}
	return s, nil
}

// args binds ?1..?6 of siteListWhere; a caller that pages fills ?3 and ?4.
func (f SiteFilter) args(pattern string) []any {
	return []any{f.OwnerID, pattern, 0, 0, f.VisibleTo, f.Workspace}
}

// visibleSite is the SQL condition "site s is visible to the user bound to
// param": a personal site of the user, or a site of one of their workspaces.
func visibleSite(param string) string {
	return `((s.workspace_id IS NULL AND s.owner_id = ` + param + `)
		OR s.workspace_id IN (SELECT m.workspace_id FROM workspace_members m WHERE m.user_id = ` + param + `))`
}

// attachHostnames fills Hostnames for every site of the page in one query.
func attachHostnames(ctx context.Context, tx *sql.Tx, sites []SiteSummary) error {
	if len(sites) == 0 {
		return nil
	}
	ids := make([]string, len(sites))
	index := make(map[string]int, len(sites))
	for i, s := range sites {
		ids[i], index[s.ID] = s.ID, i
	}
	list, err := jsonList(ids)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+hostCols+
		" FROM hostnames WHERE site_id IN (SELECT value FROM json_each(?))"+hostOrder, list)
	if err != nil {
		return fmt.Errorf("list page hostnames: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return fmt.Errorf("scan hostname: %w", err)
		}
		i := index[h.SiteID]
		sites[i].Hostnames = append(sites[i].Hostnames, h)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list page hostnames: %w", err)
	}
	return nil
}

// ActiveLayout returns every active site with its current deploy, hostnames
// and ready deploys, read from one snapshot with three queries.
func (db *DB) ActiveLayout(ctx context.Context) ([]SiteLayout, error) {
	var out []SiteLayout
	err := db.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		if out, err = activeSites(ctx, tx); err != nil {
			return err
		}
		// Only verified hostnames are published: an external domain without an
		// administrator's approval must never reach by-host (no certificate).
		hosts, err := groupPairs(ctx, tx, `SELECT h.site_id, h.hostname FROM hostnames h
			JOIN sites s ON s.id = h.site_id WHERE s.deleted_at IS NULL AND h.verified = 1
			ORDER BY h.site_id, h.hostname`)
		if err != nil {
			return err
		}
		ready, err := groupPairs(ctx, tx, `SELECT d.site_id, d.id FROM deploys d
			JOIN sites s ON s.id = d.site_id WHERE s.deleted_at IS NULL AND d.status = 'ready'
			ORDER BY d.site_id, d.created_at, d.id`)
		if err != nil {
			return err
		}
		for i := range out {
			out[i].Hostnames = orEmpty(hosts[out[i].SiteID])
			out[i].ReadyDeployIDs = orEmpty(ready[out[i].SiteID])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func activeSites(ctx context.Context, tx *sql.Tx) ([]SiteLayout, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, current_deploy_id, noindex, access_password_hash != ''
		FROM sites WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list active sites: %w", err)
	}
	defer rows.Close()
	out := []SiteLayout{}
	for rows.Next() {
		var l SiteLayout
		var current sql.NullString
		if err := rows.Scan(&l.SiteID, &current, &l.Noindex, &l.Protected); err != nil {
			return nil, fmt.Errorf("scan active site: %w", err)
		}
		l.CurrentDeployID = fromNullString(current)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list active sites: %w", err)
	}
	return out, nil
}

// groupPairs runs a (key, value) query and groups values by key.
func groupPairs(ctx context.Context, tx *sql.Tx, query string) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("layout query: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("layout scan: %w", err)
		}
		out[k] = append(out[k], v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("layout query: %w", err)
	}
	return out, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
