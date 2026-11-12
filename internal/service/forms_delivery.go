package service

import (
	"context"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/store"
)

// RunFormDelivery delivers queued submissions and binds Telegram chats
// until ctx is done. It wakes on new submissions and every few seconds.
func (s *Service) RunFormDelivery(ctx context.Context) {
	t := time.NewTicker(formPollInterval)
	defer t.Stop()
	for {
		for {
			n, err := s.DeliverDueForms(ctx)
			if err != nil {
				s.log.Error("form delivery failed", "err", err)
			}
			if err != nil || n < formDeliveryBatch {
				break
			}
		}
		if err := s.PollTelegramLinks(ctx); err != nil {
			s.log.Warn("telegram binding poll failed", "err", errText(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// DeliverDueForms sends one batch of due deliveries and returns its size.
func (s *Service) DeliverDueForms(ctx context.Context) (int, error) {
	now := s.now()
	jobs, err := s.repo.ClaimDueDeliveries(ctx, now, now.Add(formDeliveryLease), formDeliveryBatch)
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	cfg, err := s.formConfig(ctx)
	if err != nil {
		return 0, err
	}
	sem := make(chan struct{}, formDeliveryWorkers)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j store.DeliveryJob) {
			defer func() { <-sem; wg.Done() }()
			s.deliver(ctx, cfg, j)
		}(j)
	}
	wg.Wait()
	return len(jobs), nil
}

func ownerLang(lang string) string {
	if lang == "en" {
		return "en"
	}
	return "ru"
}

func (s *Service) deliver(ctx context.Context, cfg formConfig, j store.DeliveryJob) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), formDeliveryTimeout)
	defer cancel()
	sub := j.Submission
	msg := forms.Message{ID: sub.ID, SiteID: sub.SiteID, Host: sub.Host, Form: sub.Form,
		CreatedAt: sub.CreatedAt, Fields: sub.Fields, Lang: ownerLang(j.OwnerLang)}
	err := s.send(ctx, cfg, j.Channel, msg)
	d := j.Delivery
	d.Attempts++
	now := s.now()
	switch {
	case err == nil:
		d.Status, d.SentAt, d.LastError = domain.DeliverySent, &now, ""
	case forms.IsPermanent(err) || d.Attempts >= formDeliveryMaxTries:
		d.Status, d.LastError = domain.DeliveryFailed, errText(err)
	default:
		d.Status, d.LastError = domain.DeliveryPending, errText(err)
		d.NextAttemptAt = now.Add(retryDelay(d.Attempts))
	}
	if err != nil {
		s.log.Warn("form delivery attempt failed", "site", sub.SiteID, "submission", sub.ID,
			"channel", j.Channel.ID, "kind", string(j.Channel.Kind), "attempt", d.Attempts, "err", errText(err))
	}
	if err := s.repo.FinishDelivery(context.WithoutCancel(ctx), d); err != nil {
		s.log.Error("store delivery result", "delivery", d.ID, "err", err)
	}
}

// retryDelay is 30 s × 2^(attempt−1): 30 s, 1 min, 2 min … about 2 h in total.
func retryDelay(attempt int) time.Duration {
	return formDeliveryBaseDelay << min(attempt-1, 10)
}

func (s *Service) send(ctx context.Context, cfg formConfig, ch domain.FormChannel, m forms.Message) error {
	switch ch.Kind {
	case domain.ChannelTelegram:
		if cfg.telegramToken == "" {
			return forms.Temporary("Telegram is not configured on this installation")
		}
		return s.senders.TelegramSend(ctx, cfg.telegramToken, ch.Target, m.Text())
	case domain.ChannelEmail:
		if !cfg.smtp.Configured() {
			return forms.Temporary("e-mail is not configured on this installation")
		}
		return s.senders.Email(ctx, cfg.smtp, ch.Target, m.Subject(), m.Text())
	case domain.ChannelWebhook:
		secret, err := s.sealer.Open(ch.SecretEnc, formChannelSecretCtx+ch.SiteID)
		if err != nil {
			if isUnsealable(err) {
				return forms.Permanent("the webhook secret cannot be read (restored without the key file?); rotate the secret")
			}
			return forms.Temporary("webhook secret unavailable")
		}
		body, err := m.WebhookJSON()
		if err != nil {
			return forms.Permanent("could not encode the submission")
		}
		return s.senders.Webhook(ctx, ch.Target, secret, body)
	}
	return forms.Permanent("unknown channel kind")
}

// PollTelegramLinks binds chats that opened a t.me/<bot>?start=<code> link.
// It calls the Bot API only while such bindings are pending.
func (s *Service) PollTelegramLinks(ctx context.Context) error {
	pending, err := s.repo.ListPendingTelegramLinks(ctx, s.now())
	if err != nil || len(pending) == 0 {
		return err
	}
	cfg, err := s.formConfig(ctx)
	if err != nil || cfg.telegramToken == "" {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, formDeliveryTimeout)
	defer cancel()
	updates, err := s.senders.TelegramUpdates(ctx, cfg.telegramToken, s.tgOffset)
	if err != nil {
		return err
	}
	for _, u := range updates {
		if u.ID >= s.tgOffset {
			s.tgOffset = u.ID + 1
		}
		code, ok := forms.StartCode(u.Text)
		if !ok || u.ChatID == "" {
			continue
		}
		for _, ch := range pending {
			if ch.Target == "" && forms.MatchCode(ch.VerifyHash, code) {
				s.bindTelegram(ctx, cfg, ch, u.ChatID)
				break
			}
		}
	}
	return nil
}

func (s *Service) bindTelegram(ctx context.Context, cfg formConfig, ch domain.FormChannel, chatID string) {
	ch.Target, ch.Status, ch.UpdatedAt = chatID, domain.ChannelActive, s.now()
	ch.VerifyHash, ch.VerifyExpiresAt, ch.VerifyAttempts = nil, nil, 0
	if err := s.repo.UpdateFormChannel(ctx, ch); err != nil {
		s.log.Error("bind telegram chat", "channel", ch.ID, "err", err)
		return
	}
	s.audit(ctx, Actor{}, "form.channel.verify", siteTarget(ch.SiteID), map[string]any{"channel_id": ch.ID, "kind": "telegram"})
	site, err := s.repo.GetSite(ctx, ch.SiteID)
	host := ch.SiteID
	if err == nil {
		host = s.siteHost(ctx, site)
	}
	text := "Готово: заявки с сайта " + host + " будут приходить в этот чат.\nDone: submissions from " + host + " will arrive in this chat."
	if err := s.senders.TelegramSend(ctx, cfg.telegramToken, chatID, text); err != nil {
		s.log.Warn("telegram binding confirmation failed", "channel", ch.ID, "err", errText(err))
	}
}
