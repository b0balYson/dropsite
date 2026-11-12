package mcp

import (
	"context"
	"time"
)

// FormsBackend manages the forms of the user's sites (remote server only).
type FormsBackend interface {
	GetFormSettings(ctx context.Context, ref string) (FormSettings, error)
	SetFormSettings(ctx context.Context, ref string, requireAltcha bool) (FormSettings, error)
	ListSubmissions(ctx context.Context, ref string, f SubmissionFilter) (SubmissionsPage, error)
	ExportSubmissions(ctx context.Context, ref string, f SubmissionFilter, maxRows int) (SubmissionsExport, error)
	ListChannels(ctx context.Context, ref string) (ChannelList, error)
	AddChannel(ctx context.Context, ref, kind, target string) (ChannelAdded, error)
	VerifyChannel(ctx context.Context, ref, channelID, code string) (Channel, error)
	SetChannelEnabled(ctx context.Context, ref, channelID string, enabled bool) (Channel, error)
	DeleteChannel(ctx context.Context, ref, channelID string) error
	RotateChannelSecret(ctx context.Context, ref, channelID string) (ChannelAdded, error)
}

// SubmissionFilter selects submissions. Since and Until are already parsed
// (Until exclusive); Days, if set, means the last Days days by the server
// clock and overrides Since.
type SubmissionFilter struct {
	Form   string
	Query  string
	Since  time.Time
	Until  time.Time
	Days   int
	Cursor string
	Limit  int
}

// FormSettings is the form setup of a site.
type FormSettings struct {
	SiteID        string   `json:"site_id"`
	SiteURL       string   `json:"site_url" jsonschema:"primary address of the site"`
	RequireAltcha bool     `json:"require_altcha" jsonschema:"true when submissions without a solved Altcha anti-robot check are rejected"`
	FormNames     []string `json:"form_names" jsonschema:"names of the forms that already received submissions"`
	FormEndpoint  string   `json:"form_endpoint" jsonschema:"where a form of this site posts, with <name> replaced by the form name; in the site's HTML use the relative action /_form/<name>"`
	FormSnippet   string   `json:"form_snippet" jsonschema:"ready HTML of a contact form for this site (honeypot and Altcha widget included); adapt the fields and the form name"`
	Telegram      bool     `json:"telegram_available" jsonschema:"whether Telegram channels can be added on this server"`
	TelegramBot   string   `json:"telegram_bot,omitempty" jsonschema:"the server's Telegram bot, e.g. @dropsite_bot"`
	Email         bool     `json:"email_available" jsonschema:"whether e-mail channels can be added on this server"`
	CabinetURL    string   `json:"cabinet_url" jsonschema:"web interface page with the submissions and channels of the site"`
}

// Field is one submitted field.
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Delivery is the state of a submission on one channel.
type Delivery struct {
	ChannelID string `json:"channel_id"`
	Channel   string `json:"channel" jsonschema:"the channel in words, e.g. Telegram chat 123 or e-mail ann@example.com"`
	Status    string `json:"status" jsonschema:"sent, pending, failed or cancelled"`
	Summary   string `json:"summary" jsonschema:"the state in plain words, ready to tell the user"`
	Attempts  int    `json:"attempts"`
}

// Submission is a stored form submission.
type Submission struct {
	ID         string     `json:"id"`
	Form       string     `json:"form"`
	Host       string     `json:"host" jsonschema:"site address the form was sent from"`
	CreatedAt  string     `json:"created_at" jsonschema:"UTC, RFC 3339"`
	Fields     []Field    `json:"fields"`
	Deliveries []Delivery `json:"deliveries" jsonschema:"one entry per channel the submission was sent to; empty when no channel was connected"`
}

// SubmissionsPage is one page of list_submissions.
type SubmissionsPage struct {
	Submissions []Submission `json:"submissions" jsonschema:"newest first"`
	Total       int          `json:"total" jsonschema:"number of submissions matching the filter"`
	NextCursor  string       `json:"next_cursor,omitempty" jsonschema:"pass as cursor to get the next page; empty on the last page"`
	ServerTime  string       `json:"server_time" jsonschema:"current UTC time of the server"`
	CabinetURL  string       `json:"cabinet_url" jsonschema:"web interface page with all submissions"`
}

// SubmissionsExport is a CSV export returned inline.
type SubmissionsExport struct {
	CSV        string `json:"csv" jsonschema:"CSV text (UTF-8): created_at, form, host, one column per field, _other"`
	Rows       int    `json:"rows" jsonschema:"submissions in the CSV"`
	Total      int    `json:"total" jsonschema:"submissions matching the filter"`
	Truncated  bool   `json:"truncated" jsonschema:"true when only the newest submissions fit"`
	Summary    string `json:"summary" jsonschema:"what to tell the user"`
	CabinetURL string `json:"cabinet_url" jsonschema:"web interface page with the full CSV download"`
}

// Channel is a delivery channel of a site, without secrets.
type Channel struct {
	ID       string `json:"id"`
	Kind     string `json:"kind" jsonschema:"telegram, email or webhook"`
	Target   string `json:"target" jsonschema:"chat ID, e-mail address or webhook URL; empty while a Telegram link is not pressed"`
	State    string `json:"state" jsonschema:"waiting_for_confirmation, active, off or paused"`
	Enabled  bool   `json:"enabled"`
	Summary  string `json:"summary" jsonschema:"the state in plain words, ready to tell the user"`
	PausedAt string `json:"paused_at,omitempty"`
	Failures int    `json:"consecutive_failures,omitempty"`
}

// ChannelList is the output of list_channels.
type ChannelList struct {
	Channels    []Channel `json:"channels"`
	Telegram    bool      `json:"telegram_available"`
	TelegramBot string    `json:"telegram_bot,omitempty"`
	Email       bool      `json:"email_available"`
	CabinetURL  string    `json:"cabinet_url"`
}

// ChannelAdded is a new channel, or a channel with a new secret.
type ChannelAdded struct {
	Channel Channel `json:"channel"`
	Link    string  `json:"link,omitempty" jsonschema:"Telegram link the user must open and press Start in"`
	Secret  string  `json:"secret,omitempty" jsonschema:"webhook signing secret; shown only this once"`
	// NextStep tells the model what to say and do.
	NextStep string `json:"next_step" jsonschema:"what to tell the user and do next"`
}
