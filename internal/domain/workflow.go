package domain

import "time"

// Error codes of site copies, templates, scheduled publishes and
// screenshots (change chat-workflow).
const (
	CodeTemplateNotFound    = "template_not_found"
	CodeScheduleInvalid     = "schedule_invalid"
	CodeScheduleNotPending  = "schedule_not_pending"
	CodeScheduleLimit       = "schedule_limit_reached"
	CodeRendererUnavailable = "renderer_unavailable"
)

// ScheduleStatus is the state of a scheduled publish.
type ScheduleStatus string

// Scheduled publish states.
const (
	SchedulePending   ScheduleStatus = "pending"
	ScheduleDone      ScheduleStatus = "done"
	ScheduleCancelled ScheduleStatus = "cancelled"
	ScheduleFailed    ScheduleStatus = "failed"
)

// ScheduledPublish switches a site to one of its ready releases at a given
// time.
type ScheduledPublish struct {
	ID         int64
	SiteID     string
	DeployID   string
	CreatedBy  *int64
	PublishAt  time.Time
	Status     ScheduleStatus
	Error      string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

// ErrRendererUnavailable is returned by screenshots and visual diffs when no
// renderer is configured or it cannot be reached.
var ErrRendererUnavailable = E(KindConflict, CodeRendererUnavailable,
	"screenshots are not available on this server: the administrator has not enabled the renderer, "+
		"or it is not responding; open the site link to look at it instead")
