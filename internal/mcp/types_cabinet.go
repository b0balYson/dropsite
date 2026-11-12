package mcp

import (
	"context"
	"time"
)

// SiteExtras are the access flags and addresses of a site, embedded in Site
// (the fields appear at the top level of the site object).
type SiteExtras struct {
	HiddenFromSearch  bool          `json:"hidden_from_search" jsonschema:"true when search engines are asked not to index the site"`
	PasswordProtected bool          `json:"password_protected" jsonschema:"true when visitors must enter a password to open the site"`
	ExpiresAt         string        `json:"expires_at,omitempty" jsonschema:"when the site stops working (ISO 8601 UTC), if an expiry is set"`
	RootDomain        bool          `json:"root_domain,omitempty" jsonschema:"true when the site also opens on the server's root domain"`
	OwnDomains        []DomainBrief `json:"own_domains,omitempty" jsonschema:"the user's own domains attached to the site, with their status"`
}

// DomainBrief is an own domain in a site summary.
type DomainBrief struct {
	Domain string `json:"domain"`
	Status string `json:"status" jsonschema:"active (works), pending (waits for an administrator) or rejected"`
}

// Account is the user's profile, limits and usage.
type Account struct {
	Email               string `json:"email"`
	Role                string `json:"role" jsonschema:"user or admin"`
	IsAdmin             bool   `json:"is_admin" jsonschema:"true for an administrator of this server"`
	SitesUsed           int    `json:"sites_used"`
	SitesLimit          int    `json:"sites_limit"`
	MaxSiteBytes        int64  `json:"max_site_bytes" jsonschema:"largest total size of one site"`
	MaxSiteSize         string `json:"max_site_size" jsonschema:"max_site_bytes in human units, e.g. 50 MB"`
	MaxFileBytes        int64  `json:"max_file_bytes"`
	MaxFileSize         string `json:"max_file_size"`
	MaxFiles            int    `json:"max_files" jsonschema:"largest number of files in one site"`
	KeepReleases        int    `json:"keep_releases" jsonschema:"how many releases per site are kept for rollback"`
	MaxDomainsPerSite   int    `json:"max_domains_per_site" jsonschema:"how many own domains one site may have"`
	DeployFilesMaxBytes int64  `json:"deploy_files_max_bytes" jsonschema:"largest total size of one deploy_files call"`
	CabinetURL          string `json:"cabinet_url" jsonschema:"address of the dropsite web interface"`
	RootSite            *Site  `json:"root_site,omitempty" jsonschema:"administrators only: the site on the root domain, if any"`
	Summary             string `json:"summary" jsonschema:"short summary to show the user"`
}

// AccessChange is a set_site_access request; nil and false fields stay as they are.
type AccessChange struct {
	HiddenFromSearch *bool
	Password         *string
	RemovePassword   bool
	// ExpiresAt is the first instant the site no longer works.
	ExpiresAt    *time.Time
	RemoveExpiry bool
}

// SiteAccess is the access state of a site.
type SiteAccess struct {
	SiteID            string `json:"site_id"`
	URL               string `json:"url" jsonschema:"primary address of the site"`
	HiddenFromSearch  bool   `json:"hidden_from_search"`
	PasswordProtected bool   `json:"password_protected"`
	ExpiresAt         string `json:"expires_at,omitempty" jsonschema:"when the site stops working (ISO 8601 UTC)"`
	LastDay           string `json:"last_day,omitempty" jsonschema:"last day the site works (YYYY-MM-DD, UTC)"`
	Summary           string `json:"summary"`
}

// Domain is an own domain of a site.
type Domain struct {
	Domain       string `json:"domain" jsonschema:"domain name as stored (punycode for international names)"`
	DisplayName  string `json:"display_name,omitempty" jsonschema:"the international (Unicode) form, when it differs"`
	Status       string `json:"status" jsonschema:"active (works), pending (waits for an administrator) or rejected"`
	RejectReason string `json:"reject_reason,omitempty"`
	URL          string `json:"url" jsonschema:"the site's address on this domain (works once active)"`
	AddedAt      string `json:"added_at"`
}

// DNSRecord is one record the user creates at the domain's DNS provider.
type DNSRecord struct {
	Type  string `json:"type" jsonschema:"CNAME, A or AAAA"`
	Name  string `json:"name" jsonschema:"the domain the record is for"`
	Value string `json:"value" jsonschema:"what the record points to"`
}

// DNSSetup tells the user how to point a domain at the site.
type DNSSetup struct {
	Subdomain      []DNSRecord `json:"for_subdomain" jsonschema:"record for a subdomain such as shop.example.com"`
	RootDomain     []DNSRecord `json:"for_root_domain" jsonschema:"records for a root domain such as example.com; empty when the server addresses are unknown"`
	ServerIPsKnown bool        `json:"server_ips_known"`
	Steps          string      `json:"steps" jsonschema:"plain-language steps to tell the user"`
}

// DomainList is list_domains output.
type DomainList struct {
	SiteID     string   `json:"site_id"`
	Domains    []Domain `json:"domains"`
	MaxDomains int      `json:"max_domains"`
	DNS        DNSSetup `json:"dns"`
	Summary    string   `json:"summary"`
}

// DomainAdded is add_domain output.
type DomainAdded struct {
	SiteID  string   `json:"site_id"`
	Domain  Domain   `json:"domain"`
	DNS     DNSSetup `json:"dns"`
	Summary string   `json:"summary"`
}

// DayViews is one day of statistics.
type DayViews struct {
	Date     string `json:"date" jsonschema:"YYYY-MM-DD (UTC)"`
	Views    int64  `json:"views"`
	Visitors int64  `json:"visitors" jsonschema:"unique visitors that day"`
}

// NamedViews is a page path or a traffic source with its views.
type NamedViews struct {
	Name  string `json:"name" jsonschema:"page path, or the referring site (empty name: direct visits)"`
	Views int64  `json:"views"`
}

// SiteStats is get_site_stats output.
type SiteStats struct {
	SiteID        string       `json:"site_id"`
	From          string       `json:"from" jsonschema:"first day of the period (YYYY-MM-DD, UTC)"`
	To            string       `json:"to" jsonschema:"last day of the period (today, UTC)"`
	TotalViews    int64        `json:"total_views"`
	TotalVisitors int64        `json:"total_visitors" jsonschema:"sum of daily unique visitors"`
	BusiestDay    *DayViews    `json:"busiest_day,omitempty"`
	Daily         []DayViews   `json:"daily" jsonschema:"every day of the period, oldest first"`
	TopPages      []NamedViews `json:"top_pages"`
	TopSources    []NamedViews `json:"top_sources" jsonschema:"sites visitors came from"`
	Summary       string       `json:"summary"`
}

// CounterIDs are analytics counter IDs.
type CounterIDs struct {
	YandexMetricaID   string `json:"yandex_metrica_id,omitempty" jsonschema:"Yandex Metrica counter number"`
	GoogleAnalyticsID string `json:"google_analytics_id,omitempty" jsonschema:"Google Analytics 4 measurement ID (G-XXXX)"`
}

// Counters are the analytics counters of a site.
type Counters struct {
	SiteID string     `json:"site_id"`
	Own    CounterIDs `json:"own" jsonschema:"the site's own counters"`
	Global CounterIDs `json:"global" jsonschema:"counters an administrator adds to every site (read-only)"`
	// NewDeployID is set when saving the counters republished the site.
	NewDeployID string `json:"new_deploy_id,omitempty" jsonschema:"release created to apply the counters, if any"`
	Summary     string `json:"summary"`
}

// Connection kinds.
const (
	ConnApp   = "app"
	ConnToken = "token"
)

// Connection is a connected app (OAuth) or an API token, without secrets.
type Connection struct {
	Kind        string `json:"kind" jsonschema:"app (connected through OAuth, e.g. a chat assistant) or token (API token, e.g. the CLI)"`
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Detail      string `json:"detail,omitempty" jsonschema:"for apps: the host the app returned to when connecting"`
	ConnectedAt string `json:"connected_at"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty" jsonschema:"for tokens with an expiry"`
	Current     bool   `json:"current" jsonschema:"true for the connection this assistant is using right now"`
}

// Cabinet is implemented by backends that offer the account and site
// settings tools (both the remote and the stdio server).
type Cabinet interface {
	GetAccount(ctx context.Context) (Account, error)
	UpdateSite(ctx context.Context, ref string, title, slug *string) (Site, error)
	SetSiteAccess(ctx context.Context, ref string, c AccessChange) (SiteAccess, error)
	ListDomains(ctx context.Context, ref string) (DomainList, error)
	AddDomain(ctx context.Context, ref, name string) (DomainAdded, error)
	RemoveDomain(ctx context.Context, ref, name string) error
	GetSiteStats(ctx context.Context, ref string, days int) (SiteStats, error)
	GetCounters(ctx context.Context, ref string) (Counters, error)
	SetCounters(ctx context.Context, ref string, metrica, ga *string) (Counters, error)
	ListConnections(ctx context.Context) ([]Connection, error)
	RevokeConnection(ctx context.Context, kind string, id int64) error
	SetRootSite(ctx context.Context, ref, confirm string) (RootSite, error)
	ClearRootSite(ctx context.Context, confirm string) (RootSite, error)
}

// RootSite is the outcome of set_root_site or clear_root_site.
type RootSite struct {
	RootURL string `json:"root_url" jsonschema:"address of the server's root domain"`
	Site    Site   `json:"site" jsonschema:"the site put on (or taken off) the root domain"`
	Summary string `json:"summary"`
}
