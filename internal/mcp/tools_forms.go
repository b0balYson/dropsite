package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bounds of the forms tools (the server enforces its own as well).
const (
	maxSubmissionDays = 3650
	maxExportRows     = 1000
)

type formSettingsArgs struct {
	Site          string `json:"site" jsonschema:"site ID or slug"`
	RequireAltcha bool   `json:"require_altcha" jsonschema:"true to reject submissions without a solved Altcha anti-robot check"`
}

type submissionArgs struct {
	Site   string `json:"site" jsonschema:"site ID or slug"`
	Form   string `json:"form,omitempty" jsonschema:"only this form name, e.g. lead (see form_names of get_form_settings)"`
	Days   int    `json:"days,omitempty" jsonschema:"only the last N days, counted from the server time; use this for 'this week' (7) or 'this month' (30)"`
	Since  string `json:"since,omitempty" jsonschema:"only from this date or time: YYYY-MM-DD (UTC) or RFC 3339"`
	Until  string `json:"until,omitempty" jsonschema:"only up to this date (the whole day included) or RFC 3339 time"`
	Query  string `json:"query,omitempty" jsonschema:"only submissions with a field value containing this text, e.g. a name or phone number"`
	Limit  int    `json:"limit,omitempty" jsonschema:"submissions per page, 1-100 (default 20)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page"`
}

type exportArgs struct {
	Site    string `json:"site" jsonschema:"site ID or slug"`
	Form    string `json:"form,omitempty" jsonschema:"only this form name"`
	Days    int    `json:"days,omitempty" jsonschema:"only the last N days"`
	Since   string `json:"since,omitempty" jsonschema:"only from this date or time: YYYY-MM-DD (UTC) or RFC 3339"`
	Until   string `json:"until,omitempty" jsonschema:"only up to this date (the whole day included) or RFC 3339 time"`
	Query   string `json:"query,omitempty" jsonschema:"only submissions with a field value containing this text"`
	MaxRows int    `json:"max_rows,omitempty" jsonschema:"newest submissions to include, 1-1000 (default 200)"`
}

type addChannelArgs struct {
	Site   string `json:"site" jsonschema:"site ID or slug"`
	Kind   string `json:"kind" jsonschema:"telegram, email or webhook"`
	Target string `json:"target,omitempty" jsonschema:"telegram: leave empty to get a link (or a numeric chat ID); email: the address; webhook: an https:// URL"`
}

type verifyArgs struct {
	Site      string `json:"site" jsonschema:"site ID or slug"`
	ChannelID string `json:"channel_id" jsonschema:"channel ID from add_channel or list_channels"`
	Code      string `json:"code" jsonschema:"the 6-digit code the user received by e-mail or from the Telegram bot"`
}

type enableArgs struct {
	Site      string `json:"site" jsonschema:"site ID or slug"`
	ChannelID string `json:"channel_id" jsonschema:"channel ID from list_channels"`
	Enabled   bool   `json:"enabled" jsonschema:"true to deliver submissions to the channel (also resumes a paused one), false to stop"`
}

type confirmChannelArgs struct {
	Site      string `json:"site" jsonschema:"site ID or slug"`
	ChannelID string `json:"channel_id" jsonschema:"channel ID from list_channels"`
	Confirm   string `json:"confirm" jsonschema:"the channel's ID or target (address, chat ID or URL) typed again after the user explicitly agreed"`
}

type channelDeleted struct {
	Deleted string `json:"deleted" jsonschema:"ID of the deleted channel"`
}

// ErrChannelConfirm is returned by delete_channel and rotate_channel_secret
// when confirm does not name the channel.
var ErrChannelConfirm = errors.New("nothing changed: ask the user to confirm, then call again with confirm set " +
	"to the channel's ID or target exactly as shown by list_channels")

var errNoForms = errors.New("this server cannot manage forms; use the web interface of dropsite")

func (t tools) registerForms(s *sdk.Server, o Options) {
	if !o.Forms {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "get_form_settings", Annotations: readOnly("Form settings"),
		Description: "Show how a site receives form submissions: whether the Altcha anti-robot check is required, " +
			"the names of forms that already received submissions, which channels the server offers (Telegram, " +
			"e-mail), the link to the submissions page in the web interface, and form_snippet: ready HTML of a " +
			"form that posts to this site. Use it before adding a form to a site's HTML: any <form method=\"post\" " +
			"action=\"/_form/<name>\"> on the site works, no third-party service is needed; keep the honeypot field " +
			"_hp and the Altcha widget from the snippet."}, t.getFormSettings)
	sdk.AddTool(s, &sdk.Tool{Name: "set_form_settings", Annotations: writes("Change form settings", false, true),
		Description: "Turn the Altcha anti-robot check of a site's forms on or off. When on, submissions without " +
			"a solved check are rejected, so only turn it on when the site's forms contain the Altcha widget " +
			"(see form_snippet of get_form_settings); tell the user that spam bots are then blocked."}, t.setFormSettings)
	sdk.AddTool(s, &sdk.Tool{Name: "list_submissions", Annotations: readOnly("List submissions"),
		Description: "Show the form submissions (leads, orders, messages) a site received, newest first, with " +
			"the delivery state per channel in plain words. Filter by form name, period (days for 'this week', " +
			"or since/until dates) and text in the values (a name, phone, e-mail). Show the user the fields as a " +
			"short list; if next_cursor is set and the user wants more, call again with cursor. Mention failed " +
			"deliveries and suggest list_channels."}, t.listSubmissions)
	sdk.AddTool(s, &sdk.Tool{Name: "export_submissions", Annotations: readOnly("Export submissions"),
		Description: "Return a site's submissions as CSV text (the same columns as the download in the web " +
			"interface, formulas escaped) for the user to save or paste into a spreadsheet. At most 1000 rows " +
			"and 256 KB; when truncated is true, tell the user that only the newest rows are included and give " +
			"cabinet_url, where the full CSV can be downloaded."}, t.exportSubmissions)
	sdk.AddTool(s, &sdk.Tool{Name: "list_channels", Annotations: readOnly("List delivery channels"),
		Description: "List where a site's submissions are delivered (Telegram chats, e-mail addresses, " +
			"webhooks) with their state in plain words, and which channel kinds this server offers."}, t.listChannels)
	sdk.AddTool(s, &sdk.Tool{Name: "add_channel", Annotations: writes("Add delivery channel", false, false),
		Description: "Start delivering a site's submissions to Telegram, e-mail or a webhook. " +
			"telegram without target returns a link: ask the user to open it in Telegram and press Start in " +
			"the chat that should get the submissions (for a group: add the bot to the group, then send the " +
			"/start command shown in the link there); then call list_channels to see it active. " +
			"email (target = address) sends a 6-digit code: ask the user for it and call verify_channel. " +
			"webhook (target = https URL) is active at once and returns a signing secret shown only this once: " +
			"give it to the user for the receiving system. Follow next_step in the result. If the result says " +
			"channel_unavailable, tell the user that the administrator of the dropsite server must set up " +
			"Telegram or e-mail first."}, t.addChannel)
	sdk.AddTool(s, &sdk.Tool{Name: "verify_channel", Annotations: writes("Confirm delivery channel", false, false),
		Description: "Confirm a Telegram (by chat ID) or e-mail channel with the 6-digit code the user received. " +
			"Codes work 30 minutes and 5 attempts; after that remove the channel and add it again."}, t.verifyChannel)
	sdk.AddTool(s, &sdk.Tool{Name: "set_channel_enabled", Annotations: writes("Turn channel on or off", false, true),
		Description: "Turn delivery to a channel off (queued submissions for it are dropped, they stay visible " +
			"in list_submissions) or on again. Turning on also resumes a webhook paused after repeated failures."}, t.setChannelEnabled)
	sdk.AddTool(s, &sdk.Tool{Name: "delete_channel", Annotations: writes("Delete delivery channel", true, true),
		Description: "Remove a delivery channel; submissions stay stored. Only after the user explicitly " +
			"confirmed: pass the channel's ID or target again in confirm."}, t.deleteChannel)
	sdk.AddTool(s, &sdk.Tool{Name: "rotate_channel_secret", Annotations: writes("New webhook secret", true, false),
		Description: "Issue a new signing secret for a webhook channel; the old one stops working at once, so " +
			"the receiving system must be updated. Only after the user explicitly confirmed: pass the channel's " +
			"ID or target again in confirm. The new secret is shown only this once."}, t.rotateChannelSecret)
}

func (t tools) forms(ctx context.Context, req *sdk.CallToolRequest) (FormsBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	f, ok := b.(FormsBackend)
	if !ok {
		return nil, errNoForms
	}
	return f, nil
}

// parseDay reads YYYY-MM-DD (UTC midnight; endOfDay moves it to the next
// midnight) or an RFC 3339 time.
func parseDay(name, v string, endOfDay bool) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.Parse(time.DateOnly, v); err == nil {
		if endOfDay {
			d = d.AddDate(0, 0, 1)
		}
		return d, nil
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%s must be a date such as 2026-05-01 or an RFC 3339 time such as 2026-05-01T12:00:00Z", name)
}

func makeFilter(form, query, since, until string, days int) (SubmissionFilter, error) {
	if days < 0 || days > maxSubmissionDays {
		return SubmissionFilter{}, fmt.Errorf("days must be from 1 to %d", maxSubmissionDays)
	}
	s, err := parseDay("since", since, false)
	if err != nil {
		return SubmissionFilter{}, err
	}
	u, err := parseDay("until", until, true)
	if err != nil {
		return SubmissionFilter{}, err
	}
	return SubmissionFilter{Form: strings.TrimSpace(form), Query: query, Since: s, Until: u, Days: days}, nil
}

func (t tools) getFormSettings(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, FormSettings, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, FormSettings{}, err
	}
	s, err := b.GetFormSettings(ctx, in.Site)
	if err != nil {
		return nil, FormSettings{}, err
	}
	return nil, withSnippet(s), nil
}

func (t tools) setFormSettings(ctx context.Context, req *sdk.CallToolRequest, in formSettingsArgs) (*sdk.CallToolResult, FormSettings, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, FormSettings{}, err
	}
	s, err := b.SetFormSettings(ctx, in.Site, in.RequireAltcha)
	if err != nil {
		return nil, FormSettings{}, err
	}
	return nil, withSnippet(s), nil
}

func (t tools) listSubmissions(ctx context.Context, req *sdk.CallToolRequest, in submissionArgs) (*sdk.CallToolResult, SubmissionsPage, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, SubmissionsPage{}, err
	}
	f, err := makeFilter(in.Form, in.Query, in.Since, in.Until, in.Days)
	if err != nil {
		return nil, SubmissionsPage{}, err
	}
	f.Cursor, f.Limit = in.Cursor, in.Limit
	p, err := b.ListSubmissions(ctx, in.Site, f)
	if p.Submissions == nil {
		p.Submissions = []Submission{}
	}
	return nil, p, err
}

func (t tools) exportSubmissions(ctx context.Context, req *sdk.CallToolRequest, in exportArgs) (*sdk.CallToolResult, SubmissionsExport, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, SubmissionsExport{}, err
	}
	f, err := makeFilter(in.Form, in.Query, in.Since, in.Until, in.Days)
	if err != nil {
		return nil, SubmissionsExport{}, err
	}
	if in.MaxRows < 0 || in.MaxRows > maxExportRows {
		return nil, SubmissionsExport{}, fmt.Errorf("max_rows must be from 1 to %d; for more send the user to cabinet_url", maxExportRows)
	}
	out, err := b.ExportSubmissions(ctx, in.Site, f, in.MaxRows)
	return nil, out, err
}

func (t tools) listChannels(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, ChannelList, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, ChannelList{}, err
	}
	l, err := b.ListChannels(ctx, in.Site)
	if l.Channels == nil {
		l.Channels = []Channel{}
	}
	return nil, l, err
}

func (t tools) addChannel(ctx context.Context, req *sdk.CallToolRequest, in addChannelArgs) (*sdk.CallToolResult, ChannelAdded, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, ChannelAdded{}, err
	}
	r, err := b.AddChannel(ctx, in.Site, strings.ToLower(strings.TrimSpace(in.Kind)), in.Target)
	return nil, r, err
}

func (t tools) verifyChannel(ctx context.Context, req *sdk.CallToolRequest, in verifyArgs) (*sdk.CallToolResult, Channel, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, Channel{}, err
	}
	c, err := b.VerifyChannel(ctx, in.Site, in.ChannelID, strings.TrimSpace(in.Code))
	return nil, c, err
}

func (t tools) setChannelEnabled(ctx context.Context, req *sdk.CallToolRequest, in enableArgs) (*sdk.CallToolResult, Channel, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, Channel{}, err
	}
	c, err := b.SetChannelEnabled(ctx, in.Site, in.ChannelID, in.Enabled)
	return nil, c, err
}

// confirmedChannel checks that confirm names the channel.
func confirmedChannel(ctx context.Context, b FormsBackend, in confirmChannelArgs) error {
	l, err := b.ListChannels(ctx, in.Site)
	if err != nil {
		return err
	}
	c := strings.ToLower(strings.TrimSpace(in.Confirm))
	for _, ch := range l.Channels {
		if ch.ID != strings.TrimSpace(in.ChannelID) {
			continue
		}
		if c != "" && (c == strings.ToLower(ch.ID) || (ch.Target != "" && c == strings.ToLower(ch.Target))) {
			return nil
		}
		return ErrChannelConfirm
	}
	return errors.New("not found: no such channel on this site; call list_channels to see the channel IDs")
}

func (t tools) deleteChannel(ctx context.Context, req *sdk.CallToolRequest, in confirmChannelArgs) (*sdk.CallToolResult, channelDeleted, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, channelDeleted{}, err
	}
	if err := confirmedChannel(ctx, b, in); err != nil {
		return nil, channelDeleted{}, err
	}
	if err := b.DeleteChannel(ctx, in.Site, in.ChannelID); err != nil {
		return nil, channelDeleted{}, err
	}
	return nil, channelDeleted{Deleted: strings.TrimSpace(in.ChannelID)}, nil
}

func (t tools) rotateChannelSecret(ctx context.Context, req *sdk.CallToolRequest, in confirmChannelArgs) (*sdk.CallToolResult, ChannelAdded, error) {
	b, err := t.forms(ctx, req)
	if err != nil {
		return nil, ChannelAdded{}, err
	}
	if err := confirmedChannel(ctx, b, in); err != nil {
		return nil, ChannelAdded{}, err
	}
	r, err := b.RotateChannelSecret(ctx, in.Site, in.ChannelID)
	return nil, r, err
}

// formSnippetHTML is a working form for any dropsite site.
const formSnippetHTML = `<form action="/_form/contact" method="post">
  <label>Name <input name="name" required></label>
  <label>Phone or e-mail <input name="contact" required></label>
  <label>Message <textarea name="message"></textarea></label>
  <!-- honeypot: keep it empty and hidden; bots fill it in -->
  <input name="_hp" tabindex="-1" autocomplete="off" aria-hidden="true" style="position:absolute;left:-10000px">
  <!-- page of this site to show after sending (optional) -->
  <input type="hidden" name="_next" value="/">
  <!-- anti-robot check; harmless while require_altcha is off -->
  <altcha-widget challenge="/_form/challenge"></altcha-widget>
  <button type="submit">Send</button>
</form>
<script async defer src="/_dropsite/altcha.js" type="module"></script>`

func withSnippet(s FormSettings) FormSettings {
	if s.FormSnippet == "" {
		s.FormSnippet = formSnippetHTML
	}
	if s.FormNames == nil {
		s.FormNames = []string{}
	}
	return s
}
