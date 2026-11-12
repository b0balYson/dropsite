package remote

import (
	"context"
	"errors"
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
		return errors.New(mcp.NotFoundChannel)
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
	out.FormEndpoint = mcp.FormEndpoint(v.URL)
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
		next := d.NextAttemptAt
		out.Deliveries = append(out.Deliveries, mcp.NewDelivery(mcp.DeliveryInput{
			ChannelID: strconv.FormatInt(d.ChannelID, 10), Kind: string(d.ChannelKind), Target: d.ChannelTarget,
			Status: string(d.Status), Attempts: d.Attempts, LastError: d.LastError, NextAttemptAt: &next, SentAt: d.SentAt,
		}))
	}
	return out
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
	return mcp.NewExport(e.CSV, e.Rows, e.Total, e.Truncated, b.svc.FormsPageURL(v.Site.ID)), nil
}

func toChannel(c domain.FormChannel) mcp.Channel {
	return mcp.NewChannel(mcp.ChannelInput{
		ID: strconv.FormatInt(c.ID, 10), Kind: string(c.Kind), Target: c.Target, Status: string(c.Status),
		Enabled: c.Enabled, PausedAt: c.PausedAt, Failures: c.ConsecutiveFailures,
	})
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
	return mcp.NewChannelAdded(toChannel(r.Channel), r.Link, r.Secret), nil
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
	return mcp.NewSecretRotated(toChannel(r.Channel), r.Secret), nil
}
