package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DomainSetup tells which DNS records point an own domain at a site.
type DomainSetup struct {
	SiteID      string   `json:"site_id"`
	CNAMETarget string   `json:"cname_target"`
	ServerIPs   []string `json:"server_ips"`
	MaxDomains  int      `json:"max_domains"`
}

// StatsDay is one day of statistics.
type StatsDay struct {
	Date     string `json:"date"`
	Views    int64  `json:"views"`
	Visitors int64  `json:"visitors"`
}

// StatsCount is a page or a referrer with its views.
type StatsCount struct {
	Name  string `json:"name"`
	Views int64  `json:"views"`
}

// SiteStats is the response of GET /sites/{ref}/stats.
type SiteStats struct {
	SiteID string     `json:"site_id"`
	From   string     `json:"from"`
	To     string     `json:"to"`
	Days   []StatsDay `json:"days"`
	Totals struct {
		Views    int64 `json:"views"`
		Visitors int64 `json:"visitors"`
	} `json:"totals"`
	TopPages     []StatsCount `json:"top_pages"`
	TopReferrers []StatsCount `json:"top_referrers"`
}

// CounterIDs are analytics counter IDs; "" means not set.
type CounterIDs struct {
	YandexMetricaID   string `json:"yandex_metrica_id"`
	GoogleAnalyticsID string `json:"google_analytics_id"`
}

// Counters are a site's own counters and the global ones.
type Counters struct {
	SiteID      string     `json:"site_id"`
	Own         CounterIDs `json:"own"`
	Global      CounterIDs `json:"global"`
	NewDeployID *string    `json:"new_deploy_id"`
}

// CountersUpdate changes only the non-nil fields; "" removes a counter.
type CountersUpdate struct {
	YandexMetricaID   *string `json:"yandex_metrica_id,omitempty"`
	GoogleAnalyticsID *string `json:"google_analytics_id,omitempty"`
}

// Connection is a connected app or an active API token.
type Connection struct {
	Kind        string     `json:"kind"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Detail      *string    `json:"detail"`
	ConnectedAt time.Time  `json:"connected_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Current     bool       `json:"current"`
}

// RootSite is the outcome of setting or clearing the root domain.
type RootSite struct {
	RootURL string `json:"root_url"`
	Site    Site   `json:"site"`
}

// DomainSetup returns the DNS targets of a site.
func (c *Client) DomainSetup(ctx context.Context, ref string) (*DomainSetup, error) {
	var out DomainSetup
	if err := c.doJSON(ctx, http.MethodGet, sitePath(ref)+"/domain-setup", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SiteStats returns the statistics of the last days days (0: server default).
func (c *Client) SiteStats(ctx context.Context, ref string, days int) (*SiteStats, error) {
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	var out SiteStats
	if err := c.doJSON(ctx, http.MethodGet, sitePath(ref)+"/stats", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Counters returns a site's analytics counters.
func (c *Client) Counters(ctx context.Context, ref string) (*Counters, error) {
	var out Counters
	if err := c.doJSON(ctx, http.MethodGet, sitePath(ref)+"/counters", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCounters changes a site's counters.
func (c *Client) UpdateCounters(ctx context.Context, ref string, u CountersUpdate) (*Counters, error) {
	var out Counters
	if err := c.doJSON(ctx, http.MethodPatch, sitePath(ref)+"/counters", nil, u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListConnections returns the connected apps and active API tokens.
func (c *Client) ListConnections(ctx context.Context) (*List[Connection], error) {
	var out List[Connection]
	if err := c.doJSON(ctx, http.MethodGet, "/connections", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeConnection disconnects an app or revokes a token (kind app or token).
func (c *Client) RevokeConnection(ctx context.Context, kind, id string) error {
	path := "/connections/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil, nil)
}

type rootSiteRequest struct {
	Site    string `json:"site"`
	Confirm string `json:"confirm"`
}

// SetRootSite shows a site on the root domain (administrators; confirm
// repeats the site's ID or slug).
func (c *Client) SetRootSite(ctx context.Context, ref, confirm string) (*RootSite, error) {
	var out RootSite
	if err := c.doJSON(ctx, http.MethodPut, "/root-site", nil, rootSiteRequest{Site: ref, Confirm: confirm}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ClearRootSite takes the site off the root domain (confirm repeats its ID or slug).
func (c *Client) ClearRootSite(ctx context.Context, confirm string) (*RootSite, error) {
	var out RootSite
	if err := c.doJSON(ctx, http.MethodDelete, "/root-site", url.Values{"confirm": {confirm}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
