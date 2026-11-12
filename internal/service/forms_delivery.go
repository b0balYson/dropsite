package service

import (
	"context"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/store"
)

// Delivery scheduling. A continuous pool of formDeliveryWorkers attempts
// runs at a time; a channel has at most one attempt in flight and a site at
// most formDeliveryPerSite, and claims interleave sites, so one owner with
// slow or failing receivers cannot hold up the others.
const (
	formDeliveryPerSite = 2
	// formChannelPauseAfter consecutive failed attempts pause a webhook
	// channel until its owner resumes it.
	formChannelPauseAfter = 10
)

// deliveryInflight tracks attempts in flight per channel and per site.
type deliveryInflight struct {
	mu       sync.Mutex
	channels map[int64]bool
	sites    map[string]int
}

func newDeliveryInflight() *deliveryInflight {
	return &deliveryInflight{channels: map[int64]bool{}, sites: map[string]int{}}
}

func (f *deliveryInflight) busyChannels() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, 0, len(f.channels))
	for id := range f.channels {
		out = append(out, id)
	}
	return out
}

// take marks a job as in flight unless its channel or site is at its cap.
func (f *deliveryInflight) take(j store.DeliveryJob) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.channels[j.Channel.ID] || f.sites[j.Channel.SiteID] >= formDeliveryPerSite {
		return false
	}
	f.channels[j.Channel.ID] = true
	f.sites[j.Channel.SiteID]++
	return true
}

func (f *deliveryInflight) release(j store.DeliveryJob) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.channels, j.Channel.ID)
	if f.sites[j.Channel.SiteID]--; f.sites[j.Channel.SiteID] <= 0 {
		delete(f.sites, j.Channel.SiteID)
	}
}

// claimDeliveries leases up to slots due deliveries and marks them in flight.
func (s *Service) claimDeliveries(ctx context.Context, slots int) ([]store.DeliveryJob, error) {
	now := s.now()
	return s.repo.ClaimDueDeliveries(ctx, store.DeliveryClaim{
		Now: now, LeaseUntil: now.Add(formDeliveryLease), Limit: slots,
		SkipChannels: s.inflight.busyChannels(), Accept: s.inflight.take,
	})
}

// RunFormDelivery delivers queued submissions until ctx is done, keeping up
// to formDeliveryWorkers attempts running. It wakes on new submissions, on
// finished attempts and every few seconds; Telegram bindings are polled
// separately so a slow Bot API call never stalls deliveries.
func (s *Service) RunFormDelivery(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.runTelegramPoll(ctx)
	}()
	t := time.NewTicker(formPollInterval)
	defer t.Stop()
	done := make(chan struct{}, formDeliveryWorkers)
	running := 0
	for {
		if free := formDeliveryWorkers - running; free > 0 {
			running += s.startDeliveries(ctx, free, &wg, done)
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			running--
		case <-t.C:
		case <-s.wake:
		}
	}
}

func (s *Service) startDeliveries(ctx context.Context, free int, wg *sync.WaitGroup, done chan<- struct{}) int {
	jobs, err := s.claimDeliveries(ctx, free)
	if err != nil {
		s.log.Error("form delivery claim failed", "err", err)
		return 0
	}
	if len(jobs) == 0 {
		return 0
	}
	cfg, err := s.formConfig(ctx)
	if err != nil {
		s.log.Error("form delivery config failed", "err", err)
		cfg = formConfig{} // attempts fail as "not configured" and are retried
	}
	for _, j := range jobs {
		wg.Add(1)
		go func(j store.DeliveryJob) {
			defer wg.Done()
			s.deliver(ctx, cfg, j)
			s.inflight.release(j)
			done <- struct{}{}
		}(j)
	}
	return len(jobs)
}

func (s *Service) runTelegramPoll(ctx context.Context) {
	t := time.NewTicker(formPollInterval)
	defer t.Stop()
	for {
		if err := s.PollTelegramLinks(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("telegram binding poll failed", "err", forms.ErrDetail(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// DeliverDueForms runs one claim round synchronously (tests and tools) and
// returns the number of attempts made.
func (s *Service) DeliverDueForms(ctx context.Context) (int, error) {
	jobs, err := s.claimDeliveries(ctx, formDeliveryWorkers)
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	cfg, err := s.formConfig(ctx)
	if err != nil {
		for _, j := range jobs {
			s.inflight.release(j)
		}
		return 0, err
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j store.DeliveryJob) {
			defer wg.Done()
			defer s.inflight.release(j)
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
			"channel", j.Channel.ID, "kind", string(j.Channel.Kind), "attempt", d.Attempts, "err", forms.ErrDetail(err))
	}
	if err := s.repo.FinishDelivery(context.WithoutCancel(ctx), d); err != nil {
		s.log.Error("store delivery result", "delivery", d.ID, "err", err)
	}
	s.recordChannelResult(context.WithoutCancel(ctx), j.Channel, err == nil)
}

// recordChannelResult feeds the circuit breaker of webhook and Google Sheets
// channels: their receivers are chosen by site owners, while Telegram and SMTP are shared
// by the whole installation and their outages must not pause every channel.
func (s *Service) recordChannelResult(ctx context.Context, ch domain.FormChannel, ok bool) {
	if ch.Kind != domain.ChannelWebhook && ch.Kind != domain.ChannelSheets {
		return
	}
	paused, err := s.repo.RecordChannelResult(ctx, ch.ID, ok, formChannelPauseAfter, s.now())
	if err != nil {
		s.log.Error("record channel result", "channel", ch.ID, "err", err)
		return
	}
	if paused {
		s.log.Warn("form channel paused after repeated failures", "site", ch.SiteID, "channel", ch.ID,
			"failures", formChannelPauseAfter)
		s.audit(ctx, Actor{}, "form.channel.pause", siteTarget(ch.SiteID), map[string]any{
			"channel_id": ch.ID, "kind": string(ch.Kind), "failures": formChannelPauseAfter,
		})
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
	case domain.ChannelSheets:
		return s.sendSheets(ctx, ch, m)
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
		s.log.Warn("telegram binding confirmation failed", "channel", ch.ID, "err", forms.ErrDetail(err))
	}
}
