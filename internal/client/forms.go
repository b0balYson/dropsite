package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// FormSiteSettings is the form setup of a site.
type FormSiteSettings struct {
	SiteID            string   `json:"site_id"`
	SiteURL           string   `json:"site_url"`
	RequireAltcha     bool     `json:"require_altcha"`
	FormNames         []string `json:"form_names"`
	TelegramAvailable bool     `json:"telegram_available"`
	TelegramBot       *string  `json:"telegram_bot"`
	EmailAvailable    bool     `json:"email_available"`
	CabinetURL        string   `json:"cabinet_url"`
}

// FormChannel is a delivery channel, without secrets.
type FormChannel struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Target    string     `json:"target"`
	Status    string     `json:"status"`
	Enabled   bool       `json:"enabled"`
	Failures  int        `json:"consecutive_failures"`
	PausedAt  *time.Time `json:"paused_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// FormChannelResult is a new channel, or one with a new webhook secret.
type FormChannelResult struct {
	Channel FormChannel `json:"channel"`
	Secret  string      `json:"secret"`
	Link    string      `json:"link"`
}

// FormDelivery is the state of a submission on one channel.
type FormDelivery struct {
	ChannelID     string     `json:"channel_id"`
	Kind          string     `json:"kind"`
	Target        string     `json:"target"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error"`
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	SentAt        *time.Time `json:"sent_at"`
}

// FormField is one submitted field.
type FormField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FormSubmission is a stored submission.
type FormSubmission struct {
	ID         string         `json:"id"`
	Form       string         `json:"form"`
	Host       string         `json:"host"`
	CreatedAt  time.Time      `json:"created_at"`
	Fields     []FormField    `json:"fields"`
	Deliveries []FormDelivery `json:"deliveries"`
}

// SubmissionSearch is one page of a submission search.
type SubmissionSearch struct {
	Items      []FormSubmission `json:"items"`
	Total      int              `json:"total"`
	NextCursor *string          `json:"next_cursor"`
	ServerTime time.Time        `json:"server_time"`
	CabinetURL string           `json:"cabinet_url"`
}

// SubmissionExport is CSV text of the newest matching submissions.
type SubmissionExport struct {
	CSV        string `json:"csv"`
	Rows       int    `json:"rows"`
	Total      int    `json:"total"`
	Truncated  bool   `json:"truncated"`
	CabinetURL string `json:"cabinet_url"`
}

// SubmissionFilter selects submissions; zero values mean no filter. Since
// and Until are instants (Until exclusive); Days overrides Since on the
// server clock.
type SubmissionFilter struct {
	Form   string
	Query  string
	Since  time.Time
	Until  time.Time
	Days   int
	Cursor string
	Limit  int
}

func (f SubmissionFilter) values() url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("form", f.Form)
	set("q", f.Query)
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		q.Set("until", f.Until.UTC().Format(time.RFC3339))
	}
	if f.Days > 0 {
		q.Set("days", strconv.Itoa(f.Days))
	}
	set("cursor", f.Cursor)
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q
}

func formsPath(ref string) string { return sitePath(ref) + "/forms" }

func channelPath(ref, id string) string {
	return formsPath(ref) + "/channels/" + url.PathEscape(id)
}

// FormSettings returns the form setup of a site.
func (c *Client) FormSettings(ctx context.Context, ref string) (*FormSiteSettings, error) {
	var out FormSiteSettings
	if err := c.doJSON(ctx, http.MethodGet, formsPath(ref)+"/settings", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetRequireAltcha turns the Altcha check of a site's forms on or off.
func (c *Client) SetRequireAltcha(ctx context.Context, ref string, on bool) (*FormSiteSettings, error) {
	var out FormSiteSettings
	body := map[string]bool{"require_altcha": on}
	if err := c.doJSON(ctx, http.MethodPatch, formsPath(ref)+"/settings", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchSubmissions returns a page of matching submissions, newest first.
func (c *Client) SearchSubmissions(ctx context.Context, ref string, f SubmissionFilter) (*SubmissionSearch, error) {
	var out SubmissionSearch
	if err := c.doJSON(ctx, http.MethodGet, formsPath(ref)+"/submissions/search", f.values(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExportSubmissions returns up to maxRows newest matching submissions as CSV
// text (0: the server default).
func (c *Client) ExportSubmissions(ctx context.Context, ref string, f SubmissionFilter, maxRows int) (*SubmissionExport, error) {
	q := f.values()
	q.Del("cursor")
	q.Del("limit")
	if maxRows > 0 {
		q.Set("max_rows", strconv.Itoa(maxRows))
	}
	var out SubmissionExport
	if err := c.doJSON(ctx, http.MethodGet, formsPath(ref)+"/submissions/export", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListChannels returns a site's delivery channels.
func (c *Client) ListChannels(ctx context.Context, ref string) (*List[FormChannel], error) {
	var out List[FormChannel]
	if err := c.doJSON(ctx, http.MethodGet, formsPath(ref)+"/channels", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddChannel adds a telegram, email or webhook channel.
func (c *Client) AddChannel(ctx context.Context, ref, kind, target string) (*FormChannelResult, error) {
	var out FormChannelResult
	body := map[string]string{"kind": kind, "target": target}
	if err := c.doJSON(ctx, http.MethodPost, formsPath(ref)+"/channels", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VerifyChannel confirms a channel with the code the user received.
func (c *Client) VerifyChannel(ctx context.Context, ref, id, code string) (*FormChannel, error) {
	var out FormChannel
	if err := c.doJSON(ctx, http.MethodPost, channelPath(ref, id)+"/verify", nil, map[string]string{"code": code}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetChannelEnabled turns delivery to a channel on or off.
func (c *Client) SetChannelEnabled(ctx context.Context, ref, id string, enabled bool) (*FormChannel, error) {
	var out FormChannel
	if err := c.doJSON(ctx, http.MethodPatch, channelPath(ref, id), nil, map[string]bool{"enabled": enabled}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteChannel removes a channel.
func (c *Client) DeleteChannel(ctx context.Context, ref, id string) error {
	return c.doJSON(ctx, http.MethodDelete, channelPath(ref, id), nil, nil, nil)
}

// RotateChannelSecret issues a new webhook signing secret.
func (c *Client) RotateChannelSecret(ctx context.Context, ref, id string) (*FormChannelResult, error) {
	var out FormChannelResult
	if err := c.doJSON(ctx, http.MethodPost, channelPath(ref, id)+"/secret", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
