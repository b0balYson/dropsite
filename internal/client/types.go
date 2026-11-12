package client

import "time"

// User is an account as returned by the API.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Limits are the per-user quotas the server enforces.
type Limits struct {
	MaxSiteBytes    int64 `json:"max_site_bytes"`
	MaxFileBytes    int64 `json:"max_file_bytes"`
	MaxFiles        int   `json:"max_files"`
	MaxSitesPerUser int   `json:"max_sites_per_user"`
	KeepReleases    int   `json:"keep_releases"`
}

// Me is the response of GET /me.
type Me struct {
	User      User   `json:"user"`
	Limits    Limits `json:"limits"`
	SiteCount int    `json:"site_count"`
}

// Hostname is one address of a site. Kind is default, slug, apex or
// external; Status is active, pending or rejected (only external domains are
// ever inactive). Reason explains a rejection.
type Hostname struct {
	Hostname   string     `json:"hostname"`
	Kind       string     `json:"kind"`
	Verified   bool       `json:"verified"`
	Status     string     `json:"status"`
	Reason     *string    `json:"reason"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at"`
}

// Deploy is one release of a site.
type Deploy struct {
	ID        string    `json:"id"`
	SiteID    string    `json:"site_id"`
	Source    string    `json:"source"`
	SizeBytes int64     `json:"size_bytes"`
	FileCount int       `json:"file_count"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Current   bool      `json:"current"`
	// PreviewURL is set only in wildcard TLS mode for ready releases.
	PreviewURL *string `json:"preview_url"`
}

// Site is the full site object.
type Site struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	URL           string     `json:"url"`
	URLs          []string   `json:"urls"`
	Slug          *string    `json:"slug"`
	Hostnames     []Hostname `json:"hostnames"`
	OwnerID       string     `json:"owner_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CurrentDeploy *Deploy    `json:"current_deploy"`
}

// SiteItem is the compact site object used in lists.
type SiteItem struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	URL          string     `json:"url"`
	URLs         []string   `json:"urls"`
	Slug         *string    `json:"slug"`
	OwnerID      string     `json:"owner_id"`
	OwnerEmail   string     `json:"owner_email"`
	LastDeployAt *time.Time `json:"last_deploy_at"`
	SizeBytes    int64      `json:"size_bytes"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Token is an API token's metadata; the secret value is never listed.
type Token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// List is a page of items with the total count.
type List[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

// ListOptions paginates list endpoints. Zero values use server defaults.
type ListOptions struct {
	Limit  int
	Offset int
}

// CreateSiteRequest is the body of POST /sites.
type CreateSiteRequest struct {
	Title string `json:"title"`
	Slug  string `json:"slug,omitempty"`
}

// UpdateSiteRequest is the body of PATCH /sites/{ref}. A nil field is left
// unchanged; Slug pointing at "" removes the slug.
type UpdateSiteRequest struct {
	Title *string `json:"title,omitempty"`
	Slug  *string `json:"slug,omitempty"`
}

// DeployResult is returned by deploy and rollback.
type DeployResult struct {
	DeployID string   `json:"deploy_id"`
	URL      string   `json:"url"`
	URLs     []string `json:"urls"`
	Deploy   Deploy   `json:"deploy"`
	Site     Site     `json:"site"`
}
