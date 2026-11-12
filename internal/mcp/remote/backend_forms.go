package remote

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.FormsBackend = backend{}

// formsError is toolError with a not-found text that names channels too.
func (b backend) formsError(err error) error {
	if err == nil {
		return nil
	}
	if e := domain.AsError(err); e.Kind == domain.KindNotFound {
		return errors.New("not found: no such site or channel among this user's sites; " +
			"call list_sites (or list_channels) to see valid IDs")
	}
	return b.toolError(err)
}

func parseChannelID(id string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || n <= 0 {
		return 0, domain.ErrNotFound
	}
	return n, nil
}

func (b backend) GetFormSettings(ctx context.Context, ref string) (mcp.FormSettings, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.FormSettings{}, b.formsError(err)
	}
	st, err := b.svc.GetFormSiteSettings(ctx, b.actor, v.Site.ID)
	if err != nil {
		return mcp.FormSettings{}, b.formsError(err)
	}
	return b.formSettings(ctx, v, st.RequireAltcha)
}

func (b backend) SetFormSettings(ctx context.Context, ref string, requireAltcha bool) (mcp.FormSettings, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.FormSettings{}, b.formsError(err)
	}
	st, err := b.svc.SetFormSiteSettings(ctx, b.actor, v.Site.ID, requireAltcha)
	if err != nil {
		return mcp.FormSettings{}, b.formsError(err)
	}
	return b.formSettings(ctx, v, st.RequireAltcha)
}

func (b backend) formSettings(ctx context.Context, v service.SiteView, requireAltcha bool) (mcp.FormSettings, error) {
	names, err := b.svc.ListFormNames(ctx, b.actor, v.Site.ID)
	if err != nil {
		return mcp.FormSettings{}, b.formsError(err)
	}
	av, err := b.svc.FormChannelAvailability(ctx)
	if err != nil {
		return mcp.FormSettings{}, b.formsError(err)
	}
	if names == nil {
		names = []string{}
	}
	out := mcp.FormSettings{
		SiteID: v.Site.ID, SiteURL: v.URL, RequireAltcha: requireAltcha, FormNames: names,
		Telegram: av.Telegram, Email: av.Email, CabinetURL: b.svc.FormsPageURL(v.Site.ID),
	}
	if v.URL != "" {
		out.FormEndpoint = strings.TrimSuffix(v.URL, "/") + "/_form/<name>"
	}
	if av.Telegram {
		out.TelegramBot = "@" + av.Bot
	}
	return out, nil
}

func (b backend) query(f mcp.SubmissionFilter) service.SubmissionQuery {
	q := service.SubmissionQuery{Form: f.Form, Text: f.Query, Since: f.Since, Until: f.Until, Cursor: f.Cursor, Limit: f.Limit}
	if f.Days > 0 {
		q.Since = b.svc.Now().AddDate(0, 0, -f.Days)
	}
	return q
}

func (b backend) ListSubmissions(ctx context.Context, ref string, f mcp.SubmissionFilter) (mcp.SubmissionsPage, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.SubmissionsPage{}, b.formsError(err)
	}
	p, err := b.svc.SearchFormSubmissions(ctx, b.actor, v.Site.ID, b.query(f))
	if err != nil {
		return mcp.SubmissionsPage{}, b.formsError(err)
	}
	now := b.svc.Now()
	out := mcp.SubmissionsPage{
		Submissions: make([]mcp.Submission, 0, len(p.Items)), Total: p.Total, NextCursor: p.Next,
		ServerTime: stamp(&now), CabinetURL: b.svc.FormsPageURL(v.Site.ID),
	}
	for _, s := range p.Items {
		out.Submissions = append(out.Submissions, toSubmission(s))
	}
	return out, nil
}

func toSubmission(s domain.FormSubmission) mcp.Submission {
	out := mcp.Submission{
		ID: strconv.FormatInt(s.ID, 10), Form: s.Form, Host: s.Host, CreatedAt: stamp(&s.CreatedAt),
		Fields: make([]mcp.Field, 0, len(s.Fields)), Deliveries: make([]mcp.Delivery, 0, len(s.Deliveries)),
	}
	for _, f := range s.Fields {
		out.Fields = append(out.Fields, mcp.Field(f))
	}
	for _, d := range s.Deliveries {
		out.Deliveries = append(out.Deliveries, mcp.Delivery{
			ChannelID: strconv.FormatInt(d.ChannelID, 10), Channel: channelWords(d.ChannelKind, d.ChannelTarget),
			Status: string(d.Status), Summary: deliveryWords(d), Attempts: d.Attempts,
		})
	}
	return out
}

func channelWords(kind domain.ChannelKind, target string) string {
	switch kind {
	case domain.ChannelTelegram:
		if target == "" {
			return "Telegram (not linked yet)"
		}
		return "Telegram chat " + target
	case domain.ChannelEmail:
		return "e-mail " + target
	}
	return "webhook " + target
}

func deliveryWords(d domain.FormDelivery) string {
	switch d.Status {
	case domain.DeliverySent:
		return "delivered at " + stamp(d.SentAt)
	case domain.DeliveryFailed:
		return fmt.Sprintf("not delivered after %d attempts: %s", d.Attempts, d.LastError)
	case domain.DeliveryCancelled:
		return "not sent: the channel was turned off or removed"
	}
	if d.Attempts == 0 {
		return "waiting to be sent"
	}
	return fmt.Sprintf("not delivered yet: %d attempts failed (%s); next try at %s",
		d.Attempts, d.LastError, stamp(&d.NextAttemptAt))
}

func (b backend) ExportSubmissions(ctx context.Context, ref string, f mcp.SubmissionFilter, maxRows int) (mcp.SubmissionsExport, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.SubmissionsExport{}, b.formsError(err)
	}
	e, err := b.svc.ExportFormSubmissionsText(ctx, b.actor, v.Site.ID, b.query(f), maxRows, 0)
	if err != nil {
		return mcp.SubmissionsExport{}, b.formsError(err)
	}
	out := mcp.SubmissionsExport{CSV: e.CSV, Rows: e.Rows, Total: e.Total, Truncated: e.Truncated,
		CabinetURL: b.svc.FormsPageURL(v.Site.ID)}
	switch {
	case e.Total == 0:
		out.Summary = "There are no submissions for this filter."
	case e.Truncated:
		out.Summary = fmt.Sprintf("Only the %d newest of %d submissions are included. The full CSV can be "+
			"downloaded on the submissions page of the web interface: %s", e.Rows, e.Total, out.CabinetURL)
	default:
		out.Summary = fmt.Sprintf("All %d submissions are included.", e.Rows)
	}
	return out, nil
}

func toChannel(c domain.FormChannel) mcp.Channel {
	out := mcp.Channel{
		ID: strconv.FormatInt(c.ID, 10), Kind: string(c.Kind), Target: c.Target, Enabled: c.Enabled,
		PausedAt: stamp(c.PausedAt), Failures: c.ConsecutiveFailures,
	}
	what := channelWords(c.Kind, c.Target)
	switch {
	case c.Status == domain.ChannelPending:
		out.State = "waiting_for_confirmation"
		if c.Kind == domain.ChannelTelegram && c.Target == "" {
			out.Summary = what + ": waiting until the user opens the bot link and presses Start; " +
				"if the link expired, delete the channel and add it again"
		} else {
			out.Summary = what + ": waiting for the confirmation code (verify_channel)"
		}
	case c.PausedAt != nil:
		out.State = "paused"
		out.Summary = fmt.Sprintf("%s: paused at %s after %d failed deliveries in a row; submissions wait. "+
			"Fix the receiver, then turn the channel on (set_channel_enabled) to resume",
			what, stamp(c.PausedAt), c.ConsecutiveFailures)
	case !c.Enabled:
		out.State = "off"
		out.Summary = what + ": turned off, nothing is sent here"
	default:
		out.State = "active"
		out.Summary = what + ": active, new submissions are delivered here"
	}
	return out
}

func (b backend) ListChannels(ctx context.Context, ref string) (mcp.ChannelList, error) {
	v, err := b.svc.GetSite(ctx, b.actor, ref)
	if err != nil {
		return mcp.ChannelList{}, b.formsError(err)
	}
	chs, err := b.svc.ListFormChannels(ctx, b.actor, v.Site.ID)
	if err != nil {
		return mcp.ChannelList{}, b.formsError(err)
	}
	av, err := b.svc.FormChannelAvailability(ctx)
	if err != nil {
		return mcp.ChannelList{}, b.formsError(err)
	}
	out := mcp.ChannelList{Channels: make([]mcp.Channel, 0, len(chs)), Telegram: av.Telegram, Email: av.Email,
		CabinetURL: b.svc.FormsPageURL(v.Site.ID)}
	if av.Telegram {
		out.TelegramBot = "@" + av.Bot
	}
	for _, c := range chs {
		out.Channels = append(out.Channels, toChannel(c))
	}
	return out, nil
}

func (b backend) AddChannel(ctx context.Context, ref, kind, target string) (mcp.ChannelAdded, error) {
	r, err := b.svc.AddFormChannel(ctx, b.actor, ref, domain.ChannelKind(kind), target)
	if err != nil {
		return mcp.ChannelAdded{}, b.formsError(err)
	}
	out := mcp.ChannelAdded{Channel: toChannel(r.Channel), Link: r.Link, Secret: r.Secret}
	id := out.Channel.ID
	switch {
	case r.Link != "":
		out.NextStep = "Give the user this link and ask them to open it in Telegram and press Start in the chat " +
			"that should receive the submissions (for a group: add the bot to the group first, then send there " +
			"the /start command with the code from the link). The link works for 30 minutes. Then call " +
			"list_channels to check that channel " + id + " is active."
	case r.Secret != "":
		out.NextStep = "The webhook is active. Give the user the signing secret now: it is shown only this once. " +
			"The receiving system checks the X-Dropsite-Signature header (sha256=HMAC-SHA256 of the body with " +
			"this secret). If the secret is lost, rotate_channel_secret issues a new one."
	case r.Channel.Kind == domain.ChannelEmail:
		out.NextStep = "A 6-digit code was sent to " + r.Channel.Target + ". Ask the user for it (it may be in the " +
			"spam folder) and call verify_channel with channel_id " + id + ". The code works 30 minutes."
	default:
		out.NextStep = "The bot sent a 6-digit code to Telegram chat " + r.Channel.Target + ". Ask the user for it " +
			"and call verify_channel with channel_id " + id + ". The code works 30 minutes."
	}
	return out, nil
}

func (b backend) VerifyChannel(ctx context.Context, ref, channelID, code string) (mcp.Channel, error) {
	id, err := parseChannelID(channelID)
	if err != nil {
		return mcp.Channel{}, b.formsError(err)
	}
	c, err := b.svc.VerifyFormChannel(ctx, b.actor, ref, id, code)
	if err != nil {
		return mcp.Channel{}, b.formsError(err)
	}
	return toChannel(c), nil
}

func (b backend) SetChannelEnabled(ctx context.Context, ref, channelID string, enabled bool) (mcp.Channel, error) {
	id, err := parseChannelID(channelID)
	if err != nil {
		return mcp.Channel{}, b.formsError(err)
	}
	c, err := b.svc.SetFormChannelEnabled(ctx, b.actor, ref, id, enabled)
	if err != nil {
		return mcp.Channel{}, b.formsError(err)
	}
	return toChannel(c), nil
}

func (b backend) DeleteChannel(ctx context.Context, ref, channelID string) error {
	id, err := parseChannelID(channelID)
	if err != nil {
		return b.formsError(err)
	}
	return b.formsError(b.svc.DeleteFormChannel(ctx, b.actor, ref, id))
}

func (b backend) RotateChannelSecret(ctx context.Context, ref, channelID string) (mcp.ChannelAdded, error) {
	id, err := parseChannelID(channelID)
	if err != nil {
		return mcp.ChannelAdded{}, b.formsError(err)
	}
	r, err := b.svc.RotateWebhookSecret(ctx, b.actor, ref, id)
	if err != nil {
		return mcp.ChannelAdded{}, b.formsError(err)
	}
	return mcp.ChannelAdded{Channel: toChannel(r.Channel), Secret: r.Secret,
		NextStep: "Give the user the new signing secret now: it is shown only this once, and the old secret " +
			"no longer works, so the receiving system must be updated."}, nil
}
