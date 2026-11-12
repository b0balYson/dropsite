package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Forms tools of the local server, through the API.

var _ mcp.FormsBackend = localBackend{}

func formsError(err error) error { return toolErrorFor(err, mcp.NotFoundChannel) }

// channelRef checks a channel ID before it becomes a path segment.
func channelRef(id string) (string, error) {
	id = strings.TrimSpace(id)
	if _, err := strconv.ParseUint(id, 10, 63); err != nil {
		return "", errors.New(mcp.NotFoundChannel)
	}
	return id, nil
}

func fromFormSettings(s *client.FormSiteSettings) mcp.FormSettings {
	return mcp.FormSettings{
		SiteID: s.SiteID, SiteURL: s.SiteURL, RequireAltcha: s.RequireAltcha, FormNames: s.FormNames,
		FormEndpoint: mcp.FormEndpoint(s.SiteURL), Telegram: s.TelegramAvailable, TelegramBot: deref(s.TelegramBot),
		Email: s.EmailAvailable, CabinetURL: s.CabinetURL,
	}
}

func (b localBackend) GetFormSettings(ctx context.Context, ref string) (mcp.FormSettings, error) {
	s, err := b.c.FormSettings(ctx, ref)
	if err != nil {
		return mcp.FormSettings{}, formsError(err)
	}
	return fromFormSettings(s), nil
}

func (b localBackend) SetFormSettings(ctx context.Context, ref string, requireAltcha bool) (mcp.FormSettings, error) {
	s, err := b.c.SetRequireAltcha(ctx, ref, requireAltcha)
	if err != nil {
		return mcp.FormSettings{}, formsError(err)
	}
	return fromFormSettings(s), nil
}

func submissionFilter(f mcp.SubmissionFilter) client.SubmissionFilter {
	return client.SubmissionFilter{Form: f.Form, Query: f.Query, Since: f.Since, Until: f.Until, Days: f.Days,
		Cursor: f.Cursor, Limit: f.Limit}
}

func fromSubmission(s client.FormSubmission) mcp.Submission {
	out := mcp.Submission{
		ID: s.ID, Form: s.Form, Host: s.Host, CreatedAt: stampTime(&s.CreatedAt),
		Fields: make([]mcp.Field, 0, len(s.Fields)), Deliveries: make([]mcp.Delivery, 0, len(s.Deliveries)),
	}
	for _, f := range s.Fields {
		out.Fields = append(out.Fields, mcp.Field(f))
	}
	for _, d := range s.Deliveries {
		out.Deliveries = append(out.Deliveries, mcp.NewDelivery(mcp.DeliveryInput{
			ChannelID: d.ChannelID, Kind: d.Kind, Target: d.Target, Status: d.Status, Attempts: d.Attempts,
			LastError: d.LastError, NextAttemptAt: d.NextAttemptAt, SentAt: d.SentAt,
		}))
	}
	return out
}

func (b localBackend) ListSubmissions(ctx context.Context, ref string, f mcp.SubmissionFilter) (mcp.SubmissionsPage, error) {
	p, err := b.c.SearchSubmissions(ctx, ref, submissionFilter(f))
	if err != nil {
		return mcp.SubmissionsPage{}, formsError(err)
	}
	out := mcp.SubmissionsPage{
		Submissions: make([]mcp.Submission, 0, len(p.Items)), Total: p.Total, NextCursor: deref(p.NextCursor),
		ServerTime: stampTime(&p.ServerTime), CabinetURL: p.CabinetURL,
	}
	for _, s := range p.Items {
		out.Submissions = append(out.Submissions, fromSubmission(s))
	}
	return out, nil
}

func (b localBackend) ExportSubmissions(ctx context.Context, ref string, f mcp.SubmissionFilter, maxRows int) (mcp.SubmissionsExport, error) {
	e, err := b.c.ExportSubmissions(ctx, ref, submissionFilter(f), maxRows)
	if err != nil {
		return mcp.SubmissionsExport{}, formsError(err)
	}
	return mcp.NewExport(e.CSV, e.Rows, e.Total, e.Truncated, e.CabinetURL), nil
}

func fromChannel(c client.FormChannel) mcp.Channel {
	return mcp.NewChannel(mcp.ChannelInput{ID: c.ID, Kind: c.Kind, Target: c.Target, Status: c.Status,
		Enabled: c.Enabled, PausedAt: c.PausedAt, Failures: c.Failures})
}

func (b localBackend) ListChannels(ctx context.Context, ref string) (mcp.ChannelList, error) {
	st, err := b.c.FormSettings(ctx, ref)
	if err != nil {
		return mcp.ChannelList{}, formsError(err)
	}
	l, err := b.c.ListChannels(ctx, st.SiteID)
	if err != nil {
		return mcp.ChannelList{}, formsError(err)
	}
	out := mcp.ChannelList{Channels: make([]mcp.Channel, 0, len(l.Items)), Telegram: st.TelegramAvailable,
		TelegramBot: deref(st.TelegramBot), Email: st.EmailAvailable, CabinetURL: st.CabinetURL}
	for _, c := range l.Items {
		out.Channels = append(out.Channels, fromChannel(c))
	}
	return out, nil
}

func (b localBackend) AddChannel(ctx context.Context, ref, kind, target string) (mcp.ChannelAdded, error) {
	r, err := b.c.AddChannel(ctx, ref, kind, target)
	if err != nil {
		return mcp.ChannelAdded{}, formsError(err)
	}
	return mcp.NewChannelAdded(fromChannel(r.Channel), r.Link, r.Secret), nil
}

func (b localBackend) VerifyChannel(ctx context.Context, ref, channelID, code string) (mcp.Channel, error) {
	channelID, err := channelRef(channelID)
	if err != nil {
		return mcp.Channel{}, err
	}
	c, err := b.c.VerifyChannel(ctx, ref, channelID, code)
	if err != nil {
		return mcp.Channel{}, formsError(err)
	}
	return fromChannel(*c), nil
}

func (b localBackend) SetChannelEnabled(ctx context.Context, ref, channelID string, enabled bool) (mcp.Channel, error) {
	channelID, err := channelRef(channelID)
	if err != nil {
		return mcp.Channel{}, err
	}
	c, err := b.c.SetChannelEnabled(ctx, ref, channelID, enabled)
	if err != nil {
		return mcp.Channel{}, formsError(err)
	}
	return fromChannel(*c), nil
}

func (b localBackend) DeleteChannel(ctx context.Context, ref, channelID string) error {
	channelID, err := channelRef(channelID)
	if err != nil {
		return err
	}
	return formsError(b.c.DeleteChannel(ctx, ref, channelID))
}

func (b localBackend) RotateChannelSecret(ctx context.Context, ref, channelID string) (mcp.ChannelAdded, error) {
	channelID, err := channelRef(channelID)
	if err != nil {
		return mcp.ChannelAdded{}, err
	}
	r, err := b.c.RotateChannelSecret(ctx, ref, channelID)
	if err != nil {
		return mcp.ChannelAdded{}, formsError(err)
	}
	return mcp.NewSecretRotated(fromChannel(r.Channel), r.Secret), nil
}
