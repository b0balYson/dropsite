package mcp

import (
	"context"
	"fmt"
)

// Not-found texts of the workflow tools.
const (
	NotFoundTemplate = "not found: no such template; call list_templates"
	NotFoundSchedule = "not found: no such scheduled publish among this user's sites; call list_scheduled_publishes"
)

// Template is a built-in starter template.
type Template struct {
	Name        string `json:"name" jsonschema:"pass it as template to create_site_from_template"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Schedule is a scheduled publish.
type Schedule struct {
	ID         string `json:"schedule_id" jsonschema:"pass it to cancel_scheduled_publish"`
	SiteID     string `json:"site_id"`
	SiteTitle  string `json:"site_title"`
	SiteURL    string `json:"site_url" jsonschema:"primary address of the site"`
	DeployID   string `json:"deploy_id" jsonschema:"the release that goes live"`
	PublishAt  string `json:"publish_at" jsonschema:"when it goes live, RFC 3339 UTC; tell the user in their time zone"`
	Status     string `json:"status" jsonschema:"pending, done, cancelled or failed"`
	Error      string `json:"error,omitempty" jsonschema:"why it was cancelled or failed"`
	FinishedAt string `json:"finished_at,omitempty"`
	PreviewURL string `json:"preview_url,omitempty" jsonschema:"address that already shows this release, if previews are enabled: let the user check it before it goes live"`
}

// ScheduleRequest is a schedule_publish call.
type ScheduleRequest struct {
	Site      string
	DeployID  string
	UploadID  string
	PublishAt string
}

// Screenshot is a PNG of the first screen of a release.
type Screenshot struct {
	SiteID   string `json:"site_id"`
	DeployID string `json:"deploy_id"`
	Device   string `json:"device"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	PNG      []byte `json:"-"`
}

// VisualDiff compares two releases.
type VisualDiff struct {
	SiteID         string  `json:"site_id"`
	FromDeployID   string  `json:"from_deploy_id"`
	ToDeployID     string  `json:"to_deploy_id"`
	Device         string  `json:"device"`
	ChangedPercent float64 `json:"changed_percent" jsonschema:"share of changed pixels on the first screen, 0-100"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Summary        string  `json:"summary" jsonschema:"short human summary to relay to the user"`
	PNG            []byte  `json:"-"`
}

// DiffSummary words a visual diff for the user.
func DiffSummary(pct float64) string {
	switch {
	case pct == 0:
		return "The first screen looks the same in both versions."
	case pct < 1:
		return fmt.Sprintf("Small change on the first screen (%.2f%% of pixels).", pct)
	default:
		return fmt.Sprintf("%.2f%% of the first screen changed; the changes are highlighted in red on the right.", pct)
	}
}

// WorkflowBackend copies sites, creates sites from templates, schedules
// publishes and takes screenshots. The tools fail when the backend does
// not implement it.
type WorkflowBackend interface {
	CloneSite(ctx context.Context, ref, title, slug string) (DeployResult, error)
	ListTemplates(ctx context.Context, lang string) ([]Template, error)
	CreateSiteFromTemplate(ctx context.Context, name, title, slug, lang string) (DeployResult, error)
	SchedulePublish(ctx context.Context, r ScheduleRequest) (Schedule, error)
	ListSchedules(ctx context.Context, site string) ([]Schedule, error)
	CancelSchedule(ctx context.Context, id string) (Schedule, error)
	Screenshot(ctx context.Context, site, deployID, device string) (Screenshot, error)
	CompareVersions(ctx context.Context, site, from, to, device string) (VisualDiff, error)
}
