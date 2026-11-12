package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/store"
)

// FormSubmissionFilter is re-exported for adapters.
type FormSubmissionFilter = store.FormSubmissionFilter

// ChannelResult is a channel plus values shown only once: the webhook
// signing secret and the Telegram binding link.
type ChannelResult struct {
	Channel domain.FormChannel
	Secret  string
	Link    string
}

func channelErr(code, msg string) error {
	return domain.E(domain.KindInvalid, code, msg)
}

var (
	errChannelCode = channelErr(domain.CodeChannelCodeInvalid,
		"the code is wrong or expired; remove the channel and add it again to get a new code")
	errNoTelegram = channelErr(domain.CodeChannelUnavailable,
		"Telegram is not set up on this installation; ask the administrator to add a bot token")
	errNoEmail = channelErr(domain.CodeChannelUnavailable,
		"e-mail is not set up on this installation; ask the administrator to configure SMTP")
)

// public strips stored secrets from a channel before it leaves the service.
func public(c domain.FormChannel) domain.FormChannel {
	c.SecretEnc, c.VerifyHash = "", nil
	return c
}

// auditTarget describes a channel target for the audit log. Webhook URLs may
// carry credentials in their path or query, so only the host is logged.
func auditTarget(kind domain.ChannelKind, target string) string {
	if kind != domain.ChannelWebhook {
		return target
	}
	if u, err := url.Parse(target); err == nil {
		return u.Host
	}
	return ""
}

// ListFormChannels returns the site's channels without secrets.
func (s *Service) ListFormChannels(ctx context.Context, a Actor, ref string) ([]domain.FormChannel, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	chs, err := s.repo.ListFormChannels(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	for i := range chs {
		chs[i] = public(chs[i])
	}
	return chs, nil
}

// AddFormChannel creates a channel. Telegram and e-mail channels start
// pending and receive a confirmation code; a webhook is active at once and
// gets a signing secret.
func (s *Service) AddFormChannel(ctx context.Context, a Actor, ref string, kind domain.ChannelKind, target string) (ChannelResult, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return ChannelResult{}, err
	}
	if !kind.Valid() {
		return ChannelResult{}, channelErr(domain.CodeChannelInvalid, "kind must be telegram, email or webhook")
	}
	n, err := s.repo.CountFormChannels(ctx, site.ID)
	if err != nil {
		return ChannelResult{}, err
	}
	if n >= FormChannelsPerSite {
		return ChannelResult{}, domain.E(domain.KindLimit, domain.CodeChannelLimit, "a site can have at most 10 channels").
			WithParams(map[string]any{"limit": FormChannelsPerSite})
	}
	if ok, retry := s.channelLimit.Allow(userKey(a.User.ID)); !ok {
		return ChannelResult{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	now := s.now()
	ch := domain.FormChannel{SiteID: site.ID, Kind: kind, Enabled: true, Status: domain.ChannelPending, CreatedAt: now, UpdatedAt: now}
	var res ChannelResult
	switch kind {
	case domain.ChannelTelegram:
		res, err = s.prepareTelegram(ctx, site, &ch, strings.TrimSpace(target))
	case domain.ChannelEmail:
		err = s.prepareEmail(ctx, site, &ch, target)
	case domain.ChannelWebhook:
		res.Secret, err = s.prepareWebhook(&ch, target)
	}
	if err != nil {
		return ChannelResult{}, err
	}
	id, err := s.repo.CreateFormChannel(ctx, ch)
	if err != nil {
		return ChannelResult{}, err
	}
	ch.ID = id
	res.Channel = public(ch)
	s.audit(ctx, a, "form.channel.create", siteTarget(site.ID), map[string]any{
		"channel_id": id, "kind": string(kind), "target": auditTarget(kind, ch.Target),
	})
	return res, nil
}

func (s *Service) setCode(ch *domain.FormChannel, code string) {
	exp := s.now().Add(FormCodeTTL)
	ch.VerifyHash, ch.VerifyExpiresAt, ch.VerifyAttempts = forms.HashCode(code), &exp, 0
}

func (s *Service) siteHost(ctx context.Context, site domain.Site) string {
	v, err := s.view(ctx, site)
	if err != nil || v.URL == "" {
		return site.ID + "." + s.cfg.BaseDomain
	}
	return strings.TrimSuffix(strings.TrimPrefix(v.URL, "https://"), "/")
}

func (s *Service) prepareTelegram(ctx context.Context, site domain.Site, ch *domain.FormChannel, chatID string) (ChannelResult, error) {
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return ChannelResult{}, err
	}
	if cfg.telegramToken == "" || cfg.botName == "" {
		return ChannelResult{}, errNoTelegram
	}
	if chatID == "" {
		code := forms.LinkCode()
		s.setCode(ch, code)
		return ChannelResult{Link: "https://t.me/" + cfg.botName + "?start=" + code}, nil
	}
	if !forms.ValidChatID(chatID) {
		return ChannelResult{}, channelErr(domain.CodeChannelInvalid, "chat ID must be a number such as 123456789 or -1001234567890")
	}
	code := forms.DigitCode()
	text := fmt.Sprintf("dropsite: код подтверждения / confirmation code %s\n%s", code, s.siteHost(ctx, site))
	if err := s.senders.TelegramSend(ctx, cfg.telegramToken, chatID, text); err != nil {
		return ChannelResult{}, channelErr(domain.CodeChannelInvalid,
			"the bot could not write to this chat: open the bot in Telegram and press Start (or add it to the group) first")
	}
	ch.Target = chatID
	s.setCode(ch, code)
	return ChannelResult{}, nil
}

func (s *Service) prepareEmail(ctx context.Context, site domain.Site, ch *domain.FormChannel, target string) error {
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.smtp.Configured() {
		return errNoEmail
	}
	addr, ok := forms.ParseEmail(target)
	if !ok {
		return channelErr(domain.CodeChannelInvalid, "enter a plain e-mail address such as sales@example.com")
	}
	code := forms.DigitCode()
	host := s.siteHost(ctx, site)
	body := fmt.Sprintf("Код подтверждения: %s\nConfirmation code: %s\n\n%s\n", code, code, host)
	if err := s.senders.Email(ctx, cfg.smtp, addr, "dropsite: "+host, body); err != nil {
		s.log.Warn("form channel confirmation e-mail failed", "site", site.ID, "err", errText(err))
		return channelErr(domain.CodeChannelUnavailable, "the confirmation e-mail could not be sent: "+errText(err))
	}
	ch.Target = addr
	s.setCode(ch, code)
	return nil
}

func (s *Service) prepareWebhook(ch *domain.FormChannel, target string) (string, error) {
	u, err := forms.ValidateWebhookURL(target, s.cfg.FormWebhookAllow)
	if err != nil {
		return "", channelErr(domain.CodeChannelInvalid, err.Error())
	}
	secret := forms.NewWebhookSecret()
	ch.Target, ch.Status = u, domain.ChannelActive
	ch.SecretEnc = s.sealer.Seal(secret, formChannelSecretCtx+ch.SiteID)
	return secret, nil
}

func (s *Service) channelOf(ctx context.Context, a Actor, ref string, id int64) (domain.Site, domain.FormChannel, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return domain.Site{}, domain.FormChannel{}, err
	}
	ch, err := s.repo.GetFormChannel(ctx, site.ID, id)
	if err != nil {
		return domain.Site{}, domain.FormChannel{}, err
	}
	return site, ch, nil
}

// VerifyFormChannel activates a pending channel with the code sent to it.
func (s *Service) VerifyFormChannel(ctx context.Context, a Actor, ref string, id int64, code string) (domain.FormChannel, error) {
	site, ch, err := s.channelOf(ctx, a, ref, id)
	if err != nil {
		return domain.FormChannel{}, err
	}
	now := s.now()
	if ch.Status != domain.ChannelPending || ch.Target == "" || len(ch.VerifyHash) == 0 ||
		ch.VerifyExpiresAt == nil || !now.Before(*ch.VerifyExpiresAt) {
		return domain.FormChannel{}, errChannelCode
	}
	ch.UpdatedAt = now
	if !forms.MatchCode(ch.VerifyHash, code) {
		ch.VerifyAttempts++
		if ch.VerifyAttempts >= FormCodeMaxAttempts {
			ch.VerifyHash, ch.VerifyExpiresAt = nil, nil
		}
		if err := s.repo.UpdateFormChannel(ctx, ch); err != nil {
			return domain.FormChannel{}, err
		}
		return domain.FormChannel{}, errChannelCode
	}
	ch.Status, ch.VerifyHash, ch.VerifyExpiresAt, ch.VerifyAttempts = domain.ChannelActive, nil, nil, 0
	if err := s.repo.UpdateFormChannel(ctx, ch); err != nil {
		return domain.FormChannel{}, err
	}
	s.audit(ctx, a, "form.channel.verify", siteTarget(site.ID), map[string]any{"channel_id": id, "kind": string(ch.Kind)})
	return public(ch), nil
}

// SetFormChannelEnabled switches delivery to a channel on or off.
func (s *Service) SetFormChannelEnabled(ctx context.Context, a Actor, ref string, id int64, enabled bool) (domain.FormChannel, error) {
	site, ch, err := s.channelOf(ctx, a, ref, id)
	if err != nil {
		return domain.FormChannel{}, err
	}
	if ch.Enabled == enabled {
		return public(ch), nil
	}
	ch.Enabled, ch.UpdatedAt = enabled, s.now()
	if err := s.repo.UpdateFormChannel(ctx, ch); err != nil {
		return domain.FormChannel{}, err
	}
	s.audit(ctx, a, "form.channel.update", siteTarget(site.ID), map[string]any{"channel_id": id, "enabled": enabled})
	return public(ch), nil
}

// DeleteFormChannel removes a channel and its pending deliveries.
func (s *Service) DeleteFormChannel(ctx context.Context, a Actor, ref string, id int64) error {
	site, ch, err := s.channelOf(ctx, a, ref, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteFormChannel(ctx, site.ID, id); err != nil {
		return err
	}
	s.audit(ctx, a, "form.channel.delete", siteTarget(site.ID), map[string]any{"channel_id": id, "kind": string(ch.Kind)})
	return nil
}

// RotateWebhookSecret issues a new signing secret for a webhook channel.
func (s *Service) RotateWebhookSecret(ctx context.Context, a Actor, ref string, id int64) (ChannelResult, error) {
	site, ch, err := s.channelOf(ctx, a, ref, id)
	if err != nil {
		return ChannelResult{}, err
	}
	if ch.Kind != domain.ChannelWebhook {
		return ChannelResult{}, channelErr(domain.CodeChannelInvalid, "only webhook channels have a secret")
	}
	secret := forms.NewWebhookSecret()
	ch.SecretEnc, ch.UpdatedAt = s.sealer.Seal(secret, formChannelSecretCtx+ch.SiteID), s.now()
	if err := s.repo.UpdateFormChannel(ctx, ch); err != nil {
		return ChannelResult{}, err
	}
	s.audit(ctx, a, "form.channel.rotate_secret", siteTarget(site.ID), map[string]any{"channel_id": id})
	return ChannelResult{Channel: public(ch), Secret: secret}, nil
}

// GetFormSiteSettings returns the site's form options.
func (s *Service) GetFormSiteSettings(ctx context.Context, a Actor, ref string) (domain.FormSiteSettings, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return domain.FormSiteSettings{}, err
	}
	return s.repo.GetFormSiteSettings(ctx, site.ID)
}

// SetFormSiteSettings changes the site's form options.
func (s *Service) SetFormSiteSettings(ctx context.Context, a Actor, ref string, requireAltcha bool) (domain.FormSiteSettings, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return domain.FormSiteSettings{}, err
	}
	st := domain.FormSiteSettings{SiteID: site.ID, RequireAltcha: requireAltcha}
	if err := s.repo.SetFormSiteSettings(ctx, st); err != nil {
		return domain.FormSiteSettings{}, err
	}
	s.audit(ctx, a, "form.settings.update", siteTarget(site.ID), map[string]any{"require_altcha": requireAltcha})
	return st, nil
}

// ListFormSubmissions returns a page of the site's submissions.
func (s *Service) ListFormSubmissions(ctx context.Context, a Actor, ref, form string, p Page) ([]domain.FormSubmission, int, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListFormSubmissions(ctx, store.FormSubmissionFilter{SiteID: site.ID, Form: form, Page: p})
}

// ListFormNames returns the form names that received submissions.
func (s *Service) ListFormNames(ctx context.Context, a Actor, ref string) ([]string, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	return s.repo.ListFormNames(ctx, site.ID)
}

// ExportFormSubmissions writes up to 10 000 newest submissions as CSV and
// returns the site ID (for the file name).
func (s *Service) ExportFormSubmissions(ctx context.Context, a Actor, ref, form string, w io.Writer) (string, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return "", err
	}
	var all []domain.FormSubmission
	for offset := 0; offset < formExportLimit; offset += store.MaxPageSize {
		page, _, err := s.repo.ListFormSubmissions(ctx, store.FormSubmissionFilter{
			SiteID: site.ID, Form: form, Page: store.Page{Limit: store.MaxPageSize, Offset: offset},
		})
		if err != nil {
			return "", err
		}
		all = append(all, page...)
		if len(page) < store.MaxPageSize {
			break
		}
	}
	if err := forms.WriteCSV(w, all); err != nil {
		return "", fmt.Errorf("write csv: %w", err)
	}
	return site.ID, nil
}

// purgeForms applies the retention setting (called by Housekeeping).
func (s *Service) purgeForms(ctx context.Context, now time.Time) error {
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return err
	}
	if _, err := s.repo.PurgeForms(ctx, now.AddDate(0, 0, -cfg.retentionDays), now); err != nil {
		return fmt.Errorf("purge form submissions: %w", err)
	}
	return nil
}

// isUnsealable reports a secret sealed with another key.
func isUnsealable(err error) bool { return errors.Is(err, forms.ErrUnsealable) }
