package service

import (
	"context"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/ratelimit"
)

// Form intake limits.
const (
	FormPostsPerIP        = 20
	FormPostsPerIPWindow  = 10 * time.Minute
	FormPostsPerSite      = 200
	FormChallengesPerIP   = 60
	FormChannelsPerSite   = 10
	FormChannelsPerHour   = 10
	FormCodeTTL           = 30 * time.Minute
	FormCodeMaxAttempts   = 5
	formExportLimit       = 10000
	secretsKeyFile        = "secrets.key"
	formChannelSecretCtx  = "form-channel:"
	formDeliveryWorkers   = 8
	formDeliveryTimeout   = 15 * time.Second
	formDeliveryLease     = 2 * time.Minute
	formDeliveryMaxTries  = 8
	formDeliveryBaseDelay = 30 * time.Second
	formPollInterval      = 3 * time.Second
)

// FormSender delivers messages; the default implementation talks to
// Telegram, SMTP and webhooks. Tests replace it.
type FormSender interface {
	TelegramMe(ctx context.Context, token string) (string, error)
	TelegramSend(ctx context.Context, token, chatID, text string) error
	TelegramUpdates(ctx context.Context, token string, offset int64) ([]forms.Update, error)
	Email(ctx context.Context, cfg forms.SMTPConfig, to, subject, body string) error
	Webhook(ctx context.Context, target, secret string, body []byte) error
}

type netSender struct {
	telegramURL string
	hooks       *forms.WebhookClient
}

func newNetSender(telegramURL string, hooks *forms.WebhookClient) *netSender {
	return &netSender{telegramURL: telegramURL, hooks: hooks}
}

func (n *netSender) TelegramMe(ctx context.Context, token string) (string, error) {
	return forms.NewTelegram(n.telegramURL, token).GetMe(ctx)
}

func (n *netSender) TelegramSend(ctx context.Context, token, chatID, text string) error {
	return forms.NewTelegram(n.telegramURL, token).Send(ctx, chatID, text)
}

func (n *netSender) TelegramUpdates(ctx context.Context, token string, offset int64) ([]forms.Update, error) {
	return forms.NewTelegram(n.telegramURL, token).Updates(ctx, offset)
}

func (n *netSender) Email(ctx context.Context, cfg forms.SMTPConfig, to, subject, body string) error {
	return forms.SendMail(ctx, cfg, to, subject, body)
}

func (n *netSender) Webhook(ctx context.Context, target, secret string, body []byte) error {
	return n.hooks.Post(ctx, target, secret, body)
}

// formState is the forms part of Service, created in New.
type formState struct {
	senders       FormSender
	sealer        *forms.Sealer
	altcha        forms.Altcha
	escalator     *forms.Escalator
	postIPLimit   *ratelimit.Window
	postSiteLimit *ratelimit.Window
	challengeIP   *ratelimit.Window
	channelLimit  *ratelimit.Window
	wake          chan struct{}
	tgOffset      int64
	inflight      *deliveryInflight
	confirm       confirmLimits
}

func newFormState(d Deps, now func() time.Time) formState {
	sender := d.FormSender
	if sender == nil {
		hooks := forms.NewWebhookClient(forms.WebhookOptions{Allow: d.Config.FormWebhookAllow, Deny: selfPrefixes(),
			Timeout: forms.DefaultWebhookTimeout})
		sender = newNetSender(d.Config.TelegramAPIURL, hooks)
	}
	return formState{
		senders:       sender,
		escalator:     forms.NewEscalator(now),
		postIPLimit:   ratelimit.New(FormPostsPerIP, FormPostsPerIPWindow).WithClock(now),
		postSiteLimit: ratelimit.New(FormPostsPerSite, time.Hour).WithClock(now),
		challengeIP:   ratelimit.New(FormChallengesPerIP, FormPostsPerIPWindow).WithClock(now),
		channelLimit:  ratelimit.New(FormChannelsPerHour, time.Hour).WithClock(now),
		wake:          make(chan struct{}, 1),
		inflight:      newDeliveryInflight(),
		confirm:       newConfirmLimits(now),
	}
}

// startForms loads the secrets key from the state volume.
func (s *Service) startForms() error {
	key, err := forms.LoadOrCreateKey(filepath.Join(s.cfg.StateDir, secretsKeyFile))
	if err != nil {
		return err
	}
	sealer, err := forms.NewSealer(key)
	if err != nil {
		return err
	}
	s.sealer = sealer
	s.altcha = forms.Altcha{SignatureSecret: sealer.Derive("altcha-signature"), KeySecret: sealer.Derive("altcha-key")}
	return nil
}

// normalizeHost lower-cases a Host header and strips the port and a
// trailing dot; "" means invalid.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	if !domain.IsValidHostname(h) || len(h) > 253 {
		return ""
	}
	return h
}

// siteForHost resolves a visitor-facing host to an active site.
func (s *Service) siteForHost(ctx context.Context, rawHost string) (domain.Site, string, error) {
	host := normalizeHost(rawHost)
	if host == "" {
		return domain.Site{}, "", domain.ErrNotFound
	}
	site, err := s.repo.GetSiteByHost(ctx, host)
	if err != nil {
		return domain.Site{}, "", err
	}
	return site, host, nil
}

// FormPost is a parsed visitor submission.
type FormPost struct {
	Host   string // Host header as received
	Form   string
	Origin string
	IP     string
	Data   forms.Parsed
}

// FormPostResult tells the adapter whether the post was stored (spam is
// answered like success but not stored).
type FormPostResult struct {
	ID     int64
	Stored bool
}

var (
	errFormOrigin  = domain.E(domain.KindInvalid, domain.CodeFormOrigin, "the form was posted from another site")
	errFormCaptcha = domain.E(domain.KindInvalid, domain.CodeFormCaptcha,
		"the anti-robot check failed or expired; reload the page and try again")
)

// sameOrigin compares an Origin header with the request host (port included).
func sameOrigin(origin, rawHost string) bool {
	if origin == "" || origin == "null" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(u.Host, "."), strings.TrimSuffix(rawHost, "."))
}

// SubmitForm stores a visitor submission and queues its deliveries.
func (s *Service) SubmitForm(ctx context.Context, p FormPost) (FormPostResult, error) {
	site, host, err := s.siteForHost(ctx, p.Host)
	if err != nil {
		return FormPostResult{}, err
	}
	form, ok := forms.NormalizeFormName(p.Form)
	if !ok {
		return FormPostResult{}, domain.ErrNotFound
	}
	if !sameOrigin(p.Origin, p.Host) {
		return FormPostResult{}, errFormOrigin
	}
	if ok, retry := s.postIPLimit.Allow("ip:" + p.IP); !ok {
		return FormPostResult{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if ok, retry := s.postSiteLimit.Allow(site.ID); !ok {
		return FormPostResult{}, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	if p.Data.Honeypot || forms.TooFast(p.Data.RenderedAt, s.now()) {
		s.penalizeAltcha(forms.PurposeForm, p.IP, site.ID)
		return FormPostResult{}, nil
	}
	if err := s.checkAltcha(ctx, site.ID, host, p.Data.Altcha); err != nil {
		s.penalizeAltcha(forms.PurposeForm, p.IP, site.ID)
		return FormPostResult{}, err
	}
	id, err := s.repo.InsertFormSubmission(ctx, domain.FormSubmission{
		SiteID: site.ID, Host: host, Form: form, Fields: p.Data.Fields, CreatedAt: s.now(),
	})
	if err != nil {
		return FormPostResult{}, err
	}
	s.wakeDelivery()
	return FormPostResult{ID: id, Stored: true}, nil
}

func (s *Service) checkAltcha(ctx context.Context, siteID, host, payload string) error {
	st, err := s.repo.GetFormSiteSettings(ctx, siteID)
	if err != nil {
		return err
	}
	if !st.RequireAltcha {
		return nil
	}
	fresh, err := s.spendAltcha(ctx, forms.PurposeForm, host, payload)
	if err != nil {
		return err
	}
	if !fresh {
		return errFormCaptcha
	}
	return nil
}

// AltchaChallenge issues a proof-of-work challenge for the forms of a site
// host (see issueAltcha for escalation).
func (s *Service) AltchaChallenge(ctx context.Context, rawHost, ip string) (altcha.Challenge, error) {
	site, host, err := s.siteForHost(ctx, rawHost)
	if err != nil {
		return altcha.Challenge{}, err
	}
	return s.issueAltcha(ctx, forms.PurposeForm, site.ID, host, ip, 0)
}

func (s *Service) wakeDelivery() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
