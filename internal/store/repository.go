package store

import (
	"context"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/stats"
)

// Repository is the full persistence contract used by internal/service.
// *DB implements it. Conventions:
//
//   - A missing row returns domain.ErrNotFound (never sql.ErrNoRows).
//   - Unique violations return the matching domain error (CodeEmailTaken,
//     CodeSlugTaken); everything else is wrapped with context.
//   - Soft-deleted sites (deleted_at set) are invisible to every method except
//     ListSitesToPurge, MarkSitePurged and SiteIDExists.
//   - Methods that change several tables run in one transaction.
//   - Times are stored as Unix milliseconds and returned in UTC.
type Repository interface {
	UserRepository
	InviteRepository
	SessionRepository
	TokenRepository
	SiteRepository
	ExternalHostRepository
	DeployRepository
	AuditRepository
	SettingsRepository
	StatsRepository
	CounterRepository
	AccessRepository
	UserLimitsRepository
	SecretRepository
	Ping(ctx context.Context) error
}

// AccessRepository manages per-site access controls and expiry.
type AccessRepository interface {
	// FindSiteByHostname resolves an active, verified hostname to its active site.
	FindSiteByHostname(ctx context.Context, hostname string) (domain.Site, error)
	SetSiteNoindex(ctx context.Context, siteID string, on bool, now time.Time) error
	GetSiteAccess(ctx context.Context, siteID string) (domain.SiteAccess, error)
	// SetSitePassword stores the hash ("" removes protection) and increments
	// the access version, returning the new version.
	SetSitePassword(ctx context.Context, siteID, hash string, now time.Time) (int64, error)
	// SetSiteExpiry sets or clears (nil) the expiry of an active site.
	SetSiteExpiry(ctx context.Context, siteID string, expiresAt *time.Time, now time.Time) error
	// ListExpiredSites returns active sites whose expiry is at or before now.
	ListExpiredSites(ctx context.Context, now time.Time) ([]string, error)
}

// UserLimitsRepository stores per-user limit overrides.
type UserLimitsRepository interface {
	// GetUserLimits returns the overrides of a user (all nil if none).
	GetUserLimits(ctx context.Context, userID int64) (domain.LimitOverrides, error)
	// SetUserLimits replaces the overrides; all-nil removes the row.
	SetUserLimits(ctx context.Context, userID int64, o domain.LimitOverrides, now time.Time) error
}

// SecretRepository stores installation-wide random keys.
type SecretRepository interface {
	// EnsureSecret stores value under name unless a value exists, and returns
	// the stored value.
	EnsureSecret(ctx context.Context, name string, value []byte, now time.Time) ([]byte, error)
}

// UserRepository manages accounts.
type UserRepository interface {
	CountUsers(ctx context.Context) (int, error)
	// CreateUser inserts u and returns its ID; duplicate email (any case) → CodeEmailTaken.
	CreateUser(ctx context.Context, u domain.User) (int64, error)
	GetUser(ctx context.Context, id int64) (domain.User, error)
	// GetUserByEmail matches case-insensitively.
	GetUserByEmail(ctx context.Context, email string) (domain.User, error)
	ListUsers(ctx context.Context, f UserFilter) ([]UserSummary, int, error)
	SetUserStatus(ctx context.Context, id int64, status domain.UserStatus, now time.Time) error
	SetUserPassword(ctx context.Context, id int64, passwordHash string, now time.Time) error
	SetUserLang(ctx context.Context, id int64, lang string, now time.Time) error
}

// InviteRepository manages password-setup links.
type InviteRepository interface {
	// CreateInvite revokes every unused invite of inv.UserID and inserts inv, atomically.
	CreateInvite(ctx context.Context, inv domain.Invite) (int64, error)
	GetInviteByHash(ctx context.Context, hash []byte) (domain.Invite, error)
	// RedeemInvite atomically checks the invite is usable at now, marks it used,
	// stores passwordHash and sets the user active (only if the user is
	// 'invited' or 'active'; a blocked user stays blocked and the call fails
	// with CodeInviteInvalid). Unusable invite → CodeInviteInvalid.
	RedeemInvite(ctx context.Context, inviteID int64, passwordHash string, now time.Time) (domain.User, error)
	DeleteExpiredInvites(ctx context.Context, before time.Time) (int, error)
}

// SessionRepository manages UI sessions.
type SessionRepository interface {
	CreateSession(ctx context.Context, s domain.Session) error
	GetSession(ctx context.Context, id []byte) (domain.Session, error)
	TouchSession(ctx context.Context, id []byte, lastSeen, expires time.Time) error
	DeleteSession(ctx context.Context, id []byte) error
	DeleteUserSessions(ctx context.Context, userID int64) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)
}

// TokenRepository manages API tokens.
type TokenRepository interface {
	CreateToken(ctx context.Context, t domain.Token) (int64, error)
	GetTokenByHash(ctx context.Context, hash []byte) (domain.Token, error)
	// ListTokens returns the user's tokens including revoked ones, newest first.
	ListTokens(ctx context.Context, userID int64) ([]domain.Token, error)
	// RevokeToken sets revoked_at if the token belongs to userID; otherwise ErrNotFound.
	RevokeToken(ctx context.Context, userID, tokenID int64, now time.Time) error
	// RevokeUserTokens revokes every still active token of userID and returns
	// how many were revoked; earlier revocation times are kept.
	RevokeUserTokens(ctx context.Context, userID int64, now time.Time) (int, error)
	TouchToken(ctx context.Context, tokenID int64, now time.Time) error
}

// SiteRepository manages sites and their hostnames.
type SiteRepository interface {
	// SiteIDExists reports whether the ID was ever used, including tombstones.
	SiteIDExists(ctx context.Context, id string) (bool, error)
	CountActiveSites(ctx context.Context, ownerID int64) (int, error)
	// CreateSite inserts the site and its hostnames atomically. A hostname
	// already taken, or a slug label equal to any existing site ID, → CodeSlugTaken.
	CreateSite(ctx context.Context, s domain.Site, hosts []domain.Hostname) error
	GetSite(ctx context.Context, id string) (domain.Site, error)
	// FindSiteBySlug resolves a slug label (without base domain) to its active site.
	FindSiteBySlug(ctx context.Context, slug string) (domain.Site, error)
	ListSites(ctx context.Context, f SiteFilter) ([]SiteSummary, int, error)
	UpdateSiteTitle(ctx context.Context, id, title string, now time.Time) error
	// SetSlugHost replaces the site's slug hostname with host (nil removes it)
	// and returns the removed hostname, if any. Taken hostname, or a slug
	// label equal to an existing site ID → CodeSlugTaken.
	SetSlugHost(ctx context.Context, siteID string, host *domain.Hostname, now time.Time) (removed string, err error)
	// SetApexHost moves the single apex hostname to siteID and returns the
	// site that previously held it ("" if none).
	SetApexHost(ctx context.Context, siteID string, host domain.Hostname, now time.Time) (previousSiteID string, err error)
	// ClearApexHost removes the apex hostname if siteID holds it.
	ClearApexHost(ctx context.Context, siteID string, now time.Time) (removed string, err error)
	TransferSite(ctx context.Context, siteID string, newOwnerID int64, now time.Time) error
	// SoftDeleteSite sets deleted_at and deletes every hostname row of the
	// site, returning the removed hostnames.
	SoftDeleteSite(ctx context.Context, siteID string, now time.Time) ([]string, error)
	ListHostnames(ctx context.Context, siteID string) ([]domain.Hostname, error)
	// ActiveLayout returns every active site with its current deploy (nil if
	// none) and hostnames — the desired state for filesystem reconciliation.
	ActiveLayout(ctx context.Context) ([]SiteLayout, error)
	ListSitesToPurge(ctx context.Context, deletedBefore time.Time) ([]string, error)
	// MarkSitePurged deletes the site's deploy rows, statistics and counters
	// and sets purged_at.
	MarkSitePurged(ctx context.Context, siteID string, now time.Time) error
}

// ExternalHostRepository manages external domains and their review. Every
// method ignores hostnames of deleted sites.
type ExternalHostRepository interface {
	// AddExternalHost inserts h as an unverified external hostname. A deleted
	// or missing site → ErrNotFound; maxPerSite external hostnames (any
	// status) already → CodeDomainLimit; a taken name (any kind) → CodeDomainTaken.
	AddExternalHost(ctx context.Context, h domain.Hostname, maxPerSite int) error
	// RemoveExternalHost deletes the site's external hostname and returns it.
	RemoveExternalHost(ctx context.Context, siteID, hostname string, now time.Time) (domain.Hostname, error)
	// ReviewExternalHost approves or rejects the site's external hostname.
	ReviewExternalHost(ctx context.Context, r ExternalReview) (domain.Hostname, error)
	GetExternalHost(ctx context.Context, hostname string) (ExternalHost, error)
	// ListExternalHosts returns matches oldest first plus the total count.
	ListExternalHosts(ctx context.Context, f ExternalHostFilter) ([]ExternalHost, int, error)
}

// DeployRepository manages releases.
type DeployRepository interface {
	// InsertDeployAndActivate inserts d and points the site at it atomically.
	// Fails with ErrNotFound if the site is deleted.
	InsertDeployAndActivate(ctx context.Context, d domain.Deploy, now time.Time) error
	SetCurrentDeploy(ctx context.Context, siteID, deployID string, now time.Time) error
	GetDeploy(ctx context.Context, siteID, deployID string) (domain.Deploy, error)
	// ListDeploys returns the site's deploys newest first.
	ListDeploys(ctx context.Context, siteID string) ([]domain.Deploy, error)
	MarkDeploysPruned(ctx context.Context, siteID string, ids []string) error
}

// AuditRepository stores the append-only audit log.
type AuditRepository interface {
	InsertAudit(ctx context.Context, e domain.AuditEntry) error
	// ListAudit returns entries newest first plus the total matching count.
	ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, int, error)
	ListAuditActions(ctx context.Context) ([]string, error)
}

// SettingsRepository stores admin overrides.
type SettingsRepository interface {
	GetSettings(ctx context.Context) (map[string]string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// StatsRepository stores visit statistics. Days are UTC 'YYYY-MM-DD'.
type StatsRepository interface {
	StatsCursor(ctx context.Context) (stats.Cursor, error)
	// SaveStatsBatch adds b to the aggregates and stores next, atomically.
	SaveStatsBatch(ctx context.Context, b *stats.Batch, next stats.Cursor, lim StatsLimits, now time.Time) error
	// StatsSalt returns the salt of day, creating it when create is set;
	// nil when it is missing and create is not set.
	StatsSalt(ctx context.Context, day string, create bool) ([]byte, error)
	// DeleteVisitorData removes salts and visitor hashes of days before before.
	DeleteVisitorData(ctx context.Context, before string) error
	// DeleteStatsBefore removes aggregates of days before before.
	DeleteStatsBefore(ctx context.Context, before string) error
	StatsDaily(ctx context.Context, siteID, from, to string) ([]domain.DayStat, error)
	StatsTopPages(ctx context.Context, siteID, from, to string, n int) ([]domain.NamedCount, error)
	StatsTopReferrers(ctx context.Context, siteID, from, to string, n int) ([]domain.NamedCount, error)
	SitesViews(ctx context.Context, siteIDs []string, from, to string) (map[string]int64, error)
}

// CounterRepository stores per-site analytics counter IDs.
type CounterRepository interface {
	GetSiteCounters(ctx context.Context, siteID string) (domain.Counters, error)
	// SetSiteCounters stores c for an active site (ErrNotFound otherwise).
	SetSiteCounters(ctx context.Context, siteID string, c domain.Counters, now time.Time) error
}

// Page bounds list queries. Limit 0 means DefaultPageSize; it is capped at MaxPageSize.
type Page struct {
	Limit  int
	Offset int
}

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// UserFilter filters ListUsers. Query matches email substrings.
type UserFilter struct {
	Query  string
	Status domain.UserStatus
	Page
}

// UserSummary is a user row with its site count.
type UserSummary struct {
	domain.User
	SiteCount int
}

// SiteFilter filters ListSites. OwnerID nil means all owners (admin view).
// Query matches substrings of any hostname, the title and the owner email.
type SiteFilter struct {
	OwnerID *int64
	Query   string
	Page
}

// SiteSummary is a site with what list screens need, ordered by updated_at desc.
type SiteSummary struct {
	domain.Site
	OwnerEmail   string
	Hostnames    []domain.Hostname
	LastDeployAt *time.Time
	SizeBytes    int64 // size of the current deploy, 0 if none
}

// SiteLayout is the desired filesystem state of one active site.
type SiteLayout struct {
	SiteID          string
	CurrentDeployID *string
	Hostnames       []string
	ReadyDeployIDs  []string
	Noindex         bool
	Protected       bool
}

// ExternalHost is an external hostname with its site and owner.
type ExternalHost struct {
	Hostname   domain.Hostname
	SiteTitle  string
	OwnerID    int64
	OwnerEmail string
}

// ExternalHostFilter filters ListExternalHosts. Status "" means any; Query
// matches substrings of the hostname, the site title and the owner email.
type ExternalHostFilter struct {
	Status domain.HostStatus
	Query  string
	Page
}

// ExternalReview is an administrator's decision. Approve false rejects with Reason.
type ExternalReview struct {
	Hostname   string
	SiteID     string
	Approve    bool
	Reason     string
	ReviewerID int64
	At         time.Time
}

// AuditFilter filters ListAudit.
type AuditFilter struct {
	ActorID *int64
	Action  string
	From    *time.Time
	To      *time.Time
	Page
}

// AuditRow is an audit entry with the actor's email.
type AuditRow struct {
	domain.AuditEntry
	ActorEmail string
}
