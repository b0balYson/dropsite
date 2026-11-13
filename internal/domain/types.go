package domain

import "time"

// Role of a user.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// UserStatus is the lifecycle state of a user.
type UserStatus string

const (
	UserInvited UserStatus = "invited"
	UserActive  UserStatus = "active"
	UserBlocked UserStatus = "blocked"
)

// HostKind is the kind of a hostname bound to a site.
type HostKind string

const (
	HostDefault  HostKind = "default"
	HostSlug     HostKind = "slug"
	HostApex     HostKind = "apex"
	HostExternal HostKind = "external"
)

// DeploySource records where a deploy came from.
type DeploySource string

const (
	SourceUI  DeploySource = "ui"
	SourceAPI DeploySource = "api"
	SourceCLI DeploySource = "cli"
	SourceMCP DeploySource = "mcp" // stage 2
)

// Valid reports whether s is a known deploy source.
func (s DeploySource) Valid() bool {
	switch s {
	case SourceUI, SourceAPI, SourceCLI, SourceMCP:
		return true
	}
	return false
}

// DeployStatus is the state of a release.
type DeployStatus string

const (
	DeployReady  DeployStatus = "ready"
	DeployFailed DeployStatus = "failed"
	DeployPruned DeployStatus = "pruned"
)

// User is an account.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	Role         Role
	Status       UserStatus
	Lang         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// Token is an API token. Hash is SHA-256 of the full token value.
type Token struct {
	ID         int64
	UserID     int64
	Name       string
	Prefix     string
	Hash       []byte
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// Active reports whether the token can authenticate at time now.
func (t Token) Active(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return t.ExpiresAt == nil || now.Before(*t.ExpiresAt)
}

// Session is a UI login session. ID is SHA-256 of the cookie value.
type Session struct {
	ID         []byte
	UserID     int64
	CSRFSecret []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	IP         string
	UserAgent  string
}

// Invite is a one-time password-setup link. Hash is SHA-256 of the secret.
type Invite struct {
	ID        int64
	UserID    int64
	Hash      []byte
	CreatedBy int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// Usable reports whether the invite can still be redeemed at time now.
func (i Invite) Usable(now time.Time) bool {
	return i.UsedAt == nil && i.RevokedAt == nil && now.Before(i.ExpiresAt)
}

// Site is a published site. ID doubles as directory name and default label.
type Site struct {
	ID              string
	OwnerID         int64
	Title           string
	CurrentDeployID *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
	PurgedAt        *time.Time
	// Access controls (stage 2). Protected is true when a visitor password is
	// set; the hash itself is never part of Site (see SiteAccess).
	Noindex   bool
	Protected bool
	ExpiresAt *time.Time
	// WorkspaceID is the shared workspace the site belongs to; nil for a
	// personal site of OwnerID (who is always the site's creator).
	WorkspaceID *int64
}

// SiteAccess is the visitor password state of a site. Version grows on every
// password change or removal; visitor cookies are bound to it.
type SiteAccess struct {
	PasswordHash string
	Version      int64
}

// Hostname binds a host name to a site. Only verified hostnames serve the
// site; every kind except external is verified on creation. RejectReason,
// ReviewedAt and ReviewedBy record an administrator's decision on an
// external hostname.
type Hostname struct {
	Hostname     string
	SiteID       string
	Kind         HostKind
	Verified     bool
	CreatedAt    time.Time
	RejectReason string
	ReviewedAt   *time.Time
	ReviewedBy   *int64
}

// Deploy is an immutable release of a site.
type Deploy struct {
	ID        string
	SiteID    string
	CreatedBy int64
	Source    DeploySource
	SizeBytes int64
	FileCount int
	Status    DeployStatus
	CreatedAt time.Time
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	ID        int64
	ActorID   *int64
	Action    string
	Target    string
	Meta      map[string]any
	IP        string
	CreatedAt time.Time
}

// Limits are the effective quotas for a user.
type Limits struct {
	MaxSiteBytes    int64
	MaxFileBytes    int64
	MaxFiles        int
	MaxSitesPerUser int
	KeepReleases    int
}

// LimitOverrides are per-user limit values set by an administrator; nil
// fields fall back to the global effective value.
type LimitOverrides struct {
	MaxSiteBytes    *int64
	MaxFileBytes    *int64
	MaxFiles        *int64
	MaxSitesPerUser *int64
	KeepReleases    *int64
}

// Empty reports whether no limit is overridden.
func (o LimitOverrides) Empty() bool {
	return o.MaxSiteBytes == nil && o.MaxFileBytes == nil && o.MaxFiles == nil &&
		o.MaxSitesPerUser == nil && o.KeepReleases == nil
}

// Apply returns l with the overrides applied. A file limit above the site
// limit is capped at the site limit.
func (o LimitOverrides) Apply(l Limits) Limits {
	if o.MaxSiteBytes != nil {
		l.MaxSiteBytes = *o.MaxSiteBytes
	}
	if o.MaxFileBytes != nil {
		l.MaxFileBytes = *o.MaxFileBytes
	}
	if o.MaxFiles != nil {
		l.MaxFiles = int(*o.MaxFiles)
	}
	if o.MaxSitesPerUser != nil {
		l.MaxSitesPerUser = int(*o.MaxSitesPerUser)
	}
	if o.KeepReleases != nil {
		l.KeepReleases = int(*o.KeepReleases)
	}
	l.MaxFileBytes = min(l.MaxFileBytes, l.MaxSiteBytes)
	return l
}
