package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Error codes of shared workspaces (change shared-workspaces).
const (
	CodeWorkspaceNameInvalid  = "workspace_name_invalid"
	CodeWorkspaceLimit        = "workspace_limit_reached"
	CodeWorkspaceMemberExists = "workspace_member_exists"
	CodeWorkspaceUserNotFound = "workspace_user_not_found"
	CodeWorkspaceLastMember   = "workspace_last_member"
	CodeWorkspaceNotEmpty     = "workspace_not_empty"
	CodeWorkspaceMoveDenied   = "workspace_move_forbidden"
)

// Workspace limits.
const (
	MaxWorkspaceNameLength = 100
	// MaxWorkspacesPerUser caps the workspaces one user belongs to.
	MaxWorkspacesPerUser = 50
	// MaxWorkspaceMembers caps the members of one workspace.
	MaxWorkspaceMembers = 100
)

// Workspace is a shared space whose members all have owner rights on its
// sites. There are no roles: every member is equal.
type Workspace struct {
	ID        int64
	Name      string
	CreatedBy *int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// MemberCount and SiteCount (active sites) are filled by reads.
	MemberCount int
	SiteCount   int
}

// WorkspaceMember is a member of a workspace.
type WorkspaceMember struct {
	UserID  int64
	Email   string
	AddedAt time.Time
}

// Workspace errors shared by the store and the service.
var (
	ErrWorkspaceLastMember = E(KindConflict, CodeWorkspaceLastMember,
		"the last member cannot leave a workspace that still has sites: move or delete the sites first")
	ErrWorkspaceNotEmpty = E(KindConflict, CodeWorkspaceNotEmpty,
		"the workspace still has sites: move or delete them first")
	ErrWorkspaceMemberExists = E(KindConflict, CodeWorkspaceMemberExists, "this user is already a member")
	ErrWorkspaceLimit        = E(KindLimit, CodeWorkspaceLimit, "workspace limit reached")
	ErrWorkspaceMoveDenied   = E(KindConflict, CodeWorkspaceMoveDenied,
		"only the person who created the site (or an administrator) can move it")
)

// ValidateWorkspaceName trims and checks a workspace name.
func ValidateWorkspaceName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > MaxWorkspaceNameLength || strings.ContainsAny(n, "\x00\r\n") {
		return "", E(KindInvalid, CodeWorkspaceNameInvalid, "workspace name must be 1-100 characters on one line").
			WithParams(map[string]any{"max": MaxWorkspaceNameLength})
	}
	return n, nil
}
