package api

import (
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/quality"
	"github.com/b0balYson/dropsite/internal/service"
)

// Numeric IDs (users, tokens, audit entries) are sent as strings, like site
// and deploy IDs, so clients treat every ID as opaque.

type userDTO struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type userItemDTO struct {
	userDTO
	SiteCount int `json:"site_count"`
}

type limitsDTO struct {
	MaxSiteBytes    int64 `json:"max_site_bytes"`
	MaxFileBytes    int64 `json:"max_file_bytes"`
	MaxFiles        int   `json:"max_files"`
	MaxSitesPerUser int   `json:"max_sites_per_user"`
	KeepReleases    int   `json:"keep_releases"`
}

type meDTO struct {
	User      userDTO   `json:"user"`
	Limits    limitsDTO `json:"limits"`
	SiteCount int       `json:"site_count"`
	// Server-wide facts an assistant tells the user.
	MaxDomainsPerSite int    `json:"max_domains_per_site"`
	MaxInlineBytes    int64  `json:"max_inline_bytes"`
	UIURL             string `json:"ui_url"`
	// RootSite is the site on the root domain, for administrators only;
	// null otherwise or when there is none.
	RootSite *siteDTO `json:"root_site"`
}

// hostnameDTO is an address of a site. Status is active for every kind
// except external domains, which are pending or rejected until approved.
type hostnameDTO struct {
	Hostname   string     `json:"hostname"`
	Kind       string     `json:"kind"`
	Verified   bool       `json:"verified"`
	Status     string     `json:"status"`
	Reason     *string    `json:"reason"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at"`
}

type deployDTO struct {
	ID        string    `json:"id"`
	SiteID    string    `json:"site_id"`
	Source    string    `json:"source"`
	SizeBytes int64     `json:"size_bytes"`
	FileCount int       `json:"file_count"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Current   bool      `json:"current"`
	// PreviewURL is the release's own address (wildcard TLS mode, ready
	// releases only); null otherwise.
	PreviewURL *string `json:"preview_url"`
	// Draft marks the site's draft release (change staged-publish).
	Draft bool `json:"draft"`
}

type siteDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	URL           string        `json:"url"`
	URLs          []string      `json:"urls"`
	Slug          *string       `json:"slug"`
	Hostnames     []hostnameDTO `json:"hostnames"`
	OwnerID       string        `json:"owner_id"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	CurrentDeploy *deployDTO    `json:"current_deploy"`
	Noindex       bool          `json:"noindex"`
	Protected     bool          `json:"protected"`
	ExpiresAt     *time.Time    `json:"expires_at"`
	// Draft is the site's draft release, null when it has none.
	Draft         *draftDTO `json:"draft"`
	WorkspaceID   *string   `json:"workspace_id"`
	WorkspaceName *string   `json:"workspace_name"`
}

type siteItemDTO struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	URL           string        `json:"url"`
	URLs          []string      `json:"urls"`
	Slug          *string       `json:"slug"`
	OwnerID       string        `json:"owner_id"`
	OwnerEmail    string        `json:"owner_email"`
	Hostnames     []hostnameDTO `json:"hostnames"`
	LastDeployAt  *time.Time    `json:"last_deploy_at"`
	SizeBytes     int64         `json:"size_bytes"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	Noindex       bool          `json:"noindex"`
	Protected     bool          `json:"protected"`
	ExpiresAt     *time.Time    `json:"expires_at"`
	WorkspaceID   *string       `json:"workspace_id"`
	WorkspaceName *string       `json:"workspace_name"`
}

type deployResultDTO struct {
	DeployID string    `json:"deploy_id"`
	URL      string    `json:"url"`
	URLs     []string  `json:"urls"`
	Deploy   deployDTO `json:"deploy"`
	Site     siteDTO   `json:"site"`
	// Quality is the short quality report of the release, null when none.
	Quality *quality.Summary `json:"quality"`
}

type tokenDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type newTokenDTO struct {
	Token tokenDTO `json:"token"`
	Value string   `json:"value"`
}

type inviteDTO struct {
	User            userDTO    `json:"user"`
	InviteURL       string     `json:"invite_url,omitempty"`
	InviteExpiresAt *time.Time `json:"invite_expires_at,omitempty"`
	Limits          *limitsDTO `json:"limits,omitempty"`
	LimitOverrides  *overrides `json:"limit_overrides,omitempty"`
}

// overrides are personal limits; null inherits the global value.
type overrides struct {
	MaxSiteBytes    *int64 `json:"max_site_bytes"`
	MaxFileBytes    *int64 `json:"max_file_bytes"`
	MaxFiles        *int64 `json:"max_files"`
	MaxSitesPerUser *int64 `json:"max_sites_per_user"`
	KeepReleases    *int64 `json:"keep_releases"`
}

type userDetailDTO struct {
	User           userDTO   `json:"user"`
	Limits         limitsDTO `json:"limits"`
	LimitOverrides overrides `json:"limit_overrides"`
}

func toOverrides(o domain.LimitOverrides) overrides {
	return overrides{
		MaxSiteBytes: o.MaxSiteBytes, MaxFileBytes: o.MaxFileBytes, MaxFiles: o.MaxFiles,
		MaxSitesPerUser: o.MaxSitesPerUser, KeepReleases: o.KeepReleases,
	}
}

func (o overrides) domain() domain.LimitOverrides {
	return domain.LimitOverrides{
		MaxSiteBytes: o.MaxSiteBytes, MaxFileBytes: o.MaxFileBytes, MaxFiles: o.MaxFiles,
		MaxSitesPerUser: o.MaxSitesPerUser, KeepReleases: o.KeepReleases,
	}
}

func toUserDetail(ul service.UserLimits) userDetailDTO {
	return userDetailDTO{User: toUser(ul.User), Limits: toLimits(ul.Effective), LimitOverrides: toOverrides(ul.Overrides)}
}

type auditDTO struct {
	ID         string         `json:"id"`
	ActorID    *string        `json:"actor_id"`
	ActorEmail string         `json:"actor_email"`
	Action     string         `json:"action"`
	Target     string         `json:"target"`
	Meta       map[string]any `json:"meta"`
	IP         string         `json:"ip"`
	CreatedAt  time.Time      `json:"created_at"`
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

// workspaceOf returns the workspace fields of a site (both null for a
// personal site).
func workspaceOf(id *int64, name string) (*string, *string) {
	if id == nil {
		return nil, nil
	}
	s := idString(*id)
	return &s, &name
}

func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := utc(*t)
	return &v
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toUser(u domain.User) userDTO {
	return userDTO{
		ID: idString(u.ID), Email: u.Email, Role: string(u.Role),
		Status: string(u.Status), CreatedAt: utc(u.CreatedAt),
	}
}

func toLimits(l domain.Limits) limitsDTO {
	return limitsDTO{
		MaxSiteBytes: l.MaxSiteBytes, MaxFileBytes: l.MaxFileBytes, MaxFiles: l.MaxFiles,
		MaxSitesPerUser: l.MaxSitesPerUser, KeepReleases: l.KeepReleases,
	}
}

func toDeploy(d domain.Deploy, currentID *string, previewURL string) deployDTO {
	return deployDTO{
		ID: d.ID, SiteID: d.SiteID, Source: string(d.Source), SizeBytes: d.SizeBytes,
		FileCount: d.FileCount, Status: string(d.Status), CreatedAt: utc(d.CreatedAt),
		CreatedBy: idString(d.CreatedBy), Current: currentID != nil && *currentID == d.ID,
		PreviewURL: optional(previewURL),
	}
}

func toDeploys(ds []domain.Deploy, currentID *string, previewURL func(domain.Deploy) string) []deployDTO {
	out := make([]deployDTO, 0, len(ds))
	for _, d := range ds {
		out = append(out, toDeploy(d, currentID, previewURL(d)))
	}
	return out
}

func toHostname(h domain.Hostname) hostnameDTO {
	return hostnameDTO{
		Hostname: h.Hostname, Kind: string(h.Kind), Verified: h.Verified, Status: string(h.Status()),
		Reason: optional(h.RejectReason), CreatedAt: utc(h.CreatedAt), ReviewedAt: utcPtr(h.ReviewedAt),
	}
}

func toHostnames(hs []domain.Hostname) []hostnameDTO {
	out := make([]hostnameDTO, 0, len(hs))
	for _, h := range hs {
		out = append(out, toHostname(h))
	}
	return out
}

func toSite(v service.SiteView) siteDTO {
	s := siteDTO{
		ID: v.Site.ID, Title: v.Site.Title, URL: v.URL, URLs: nonNil(v.URLs), Slug: optional(v.Slug),
		Hostnames: toHostnames(v.Hostnames), OwnerID: idString(v.OwnerID),
		CreatedAt: utc(v.Site.CreatedAt), UpdatedAt: utc(v.Site.UpdatedAt),
		Noindex: v.Site.Noindex, Protected: v.Site.Protected, ExpiresAt: utcPtr(v.Site.ExpiresAt),
	}
	s.WorkspaceID, s.WorkspaceName = workspaceOf(v.Site.WorkspaceID, v.WorkspaceName)
	if v.Current != nil {
		d := toDeploy(*v.Current, &v.Current.ID, v.CurrentPreviewURL)
		s.CurrentDeploy = &d
	}
	s.Draft = toDraft(v)
	return s
}

func toSiteItems(rows []service.SiteSummaryView) []siteItemDTO {
	out := make([]siteItemDTO, 0, len(rows))
	for _, r := range rows {
		wsID, wsName := workspaceOf(r.WorkspaceID, r.WorkspaceName)
		out = append(out, siteItemDTO{
			WorkspaceID: wsID, WorkspaceName: wsName,
			ID: r.ID, Title: r.Title, URL: r.URL, URLs: nonNil(r.URLs), Slug: optional(r.Slug),
			OwnerID: idString(r.OwnerID), OwnerEmail: r.OwnerEmail, Hostnames: toHostnames(r.Hostnames),
			LastDeployAt: utcPtr(r.LastDeployAt),
			SizeBytes:    r.SizeBytes, CreatedAt: utc(r.CreatedAt), UpdatedAt: utc(r.UpdatedAt),
			Noindex: r.Noindex, Protected: r.Protected, ExpiresAt: utcPtr(r.ExpiresAt),
		})
	}
	return out
}

func toDeployResult(res service.DeployResult) deployResultDTO {
	d := toDeploy(res.Deploy, res.Site.Site.CurrentDeployID, resultPreview(res))
	d.Draft = res.Site.Draft != nil && res.Site.Draft.Deploy.ID == d.ID
	return deployResultDTO{
		DeployID: res.Deploy.ID, URL: res.Site.URL, URLs: nonNil(res.Site.URLs),
		Deploy: d, Site: toSite(res.Site), Quality: quality.Summarize(res.Quality),
	}
}

// resultPreview is the preview of the deploy in a publish or rollback result;
// that deploy is the site's current one, whose preview the view carries.
func resultPreview(res service.DeployResult) string {
	if res.Site.Current != nil && res.Site.Current.ID == res.Deploy.ID {
		return res.Site.CurrentPreviewURL
	}
	return ""
}

func toToken(t domain.Token) tokenDTO {
	return tokenDTO{
		ID: idString(t.ID), Name: t.Name, Prefix: t.Prefix, CreatedAt: utc(t.CreatedAt),
		LastUsedAt: utcPtr(t.LastUsedAt), ExpiresAt: utcPtr(t.ExpiresAt), RevokedAt: utcPtr(t.RevokedAt),
	}
}

func toUserItems(rows []service.UserSummary) []userItemDTO {
	out := make([]userItemDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, userItemDTO{userDTO: toUser(r.User), SiteCount: r.SiteCount})
	}
	return out
}

func toInvite(inv service.InviteLink) inviteDTO {
	out := inviteDTO{User: toUser(inv.User), InviteURL: inv.URL}
	if !inv.Expires.IsZero() {
		out.InviteExpiresAt = utcPtr(&inv.Expires)
	}
	return out
}

func toAudit(rows []service.AuditRow) []auditDTO {
	out := make([]auditDTO, 0, len(rows))
	for _, r := range rows {
		a := auditDTO{
			ID: idString(r.ID), ActorEmail: r.ActorEmail, Action: r.Action, Target: r.Target,
			Meta: r.Meta, IP: r.IP, CreatedAt: utc(r.CreatedAt),
		}
		if r.ActorID != nil {
			id := idString(*r.ActorID)
			a.ActorID = &id
		}
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		out = append(out, a)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
