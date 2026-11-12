package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

const botToken = "123456:ABCdefGHIjklMNOpqrSTUvwxYZ012345"

// fakeSender records deliveries; fail returns the next error per kind.
type fakeSender struct {
	mu       sync.Mutex
	telegram []string // chatID + "|" + text
	emails   []string // to + "|" + subject + "|" + body
	hooks    []string // target + "|" + body
	secrets  []string
	fail     map[string][]error
	updates  []forms.Update
}

func (f *fakeSender) next(kind string) error {
	if errs := f.fail[kind]; len(errs) > 0 {
		f.fail[kind] = errs[1:]
		return errs[0]
	}
	return nil
}

func (f *fakeSender) TelegramMe(_ context.Context, token string) (string, error) {
	if token != botToken {
		return "", forms.Permanent("telegram: 401 Unauthorized")
	}
	return "dropsite_bot", nil
}

func (f *fakeSender) TelegramSend(_ context.Context, _, chatID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.next("telegram"); err != nil {
		return err
	}
	f.telegram = append(f.telegram, chatID+"|"+text)
	return nil
}

func (f *fakeSender) TelegramUpdates(context.Context, string, int64) ([]forms.Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.updates
	f.updates = nil
	return u, nil
}

func (f *fakeSender) Email(_ context.Context, _ forms.SMTPConfig, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.next("email"); err != nil {
		return err
	}
	f.emails = append(f.emails, to+"|"+subject+"|"+body)
	return nil
}

func (f *fakeSender) Webhook(_ context.Context, target, secret string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.next("webhook"); err != nil {
		return err
	}
	f.hooks = append(f.hooks, target+"|"+string(body))
	f.secrets = append(f.secrets, secret)
	return nil
}

func formEnv(t *testing.T) (*env, *fakeSender) {
	t.Helper()
	e := newEnv(t)
	f := &fakeSender{fail: map[string][]error{}}
	e.svc.senders = f
	return e, f
}

func mkFormSite(t *testing.T, e *env, owner Actor) SiteView {
	t.Helper()
	v, err := e.svc.CreateSite(context.Background(), owner, "Promo", "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func post(host string, fields ...string) FormPost {
	p := FormPost{Host: host, Form: "lead", IP: "203.0.113.5"}
	for i := 0; i+1 < len(fields); i += 2 {
		p.Data.Fields = append(p.Data.Fields, domain.FormField{Name: fields[i], Value: fields[i+1]})
	}
	return p
}

func TestSubmitFormStoresAndQueues(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	res, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://hooks.example.com/x?token=abc")
	if err != nil || res.Secret == "" || res.Channel.SecretEnc != "" || res.Channel.Status != domain.ChannelActive {
		t.Fatalf("webhook channel %+v %v", res, err)
	}
	p := post(strings.ToUpper(host)+":443", "name", "Ann")
	p.Origin = "https://" + strings.ToUpper(host) + ":443"
	out, err := e.svc.SubmitForm(ctx, p)
	if err != nil || !out.Stored {
		t.Fatalf("submit %+v %v", out, err)
	}
	if n, err := e.svc.DeliverDueForms(ctx); err != nil || n != 1 {
		t.Fatalf("deliver %d %v", n, err)
	}
	if len(f.hooks) != 1 || !strings.Contains(f.hooks[0], `"name":"Ann"`) || f.secrets[0] != res.Secret {
		t.Fatalf("hooks %v", f.hooks)
	}
	subs, total, err := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	if err != nil || total != 1 || subs[0].Host != host || subs[0].Deliveries[0].Status != domain.DeliverySent {
		t.Fatalf("list %+v %v", subs, err)
	}
	// Audit must not contain the webhook path or token.
	rows, _, _ := e.svc.ListAudit(ctx, e.admin, AuditFilter{Action: "form.channel.create"})
	if len(rows) != 1 || rows[0].Meta["target"] != "hooks.example.com" {
		t.Fatalf("audit %+v", rows)
	}
}

func TestSubmitFormRejects(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	if _, err := e.svc.SubmitForm(ctx, post("nope."+testBase, "a", "1")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown host: %v", err)
	}
	if _, err := e.svc.SubmitForm(ctx, post("..", "a", "1")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad host: %v", err)
	}
	bad := post(host, "a", "1")
	bad.Form = "../x"
	if _, err := e.svc.SubmitForm(ctx, bad); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bad form name: %v", err)
	}
	cross := post(host, "a", "1")
	cross.Origin = "https://evil.example"
	_, err := e.svc.SubmitForm(ctx, cross)
	wantCode(t, err, domain.CodeFormOrigin)

	hp := post(host, "a", "1")
	hp.Data.Honeypot = true
	if out, err := e.svc.SubmitForm(ctx, hp); err != nil || out.Stored {
		t.Fatalf("honeypot %+v %v", out, err)
	}
	fast := post(host, "a", "1")
	now := e.clock.Now()
	fast.Data.RenderedAt = &now
	if out, err := e.svc.SubmitForm(ctx, fast); err != nil || out.Stored {
		t.Fatalf("too fast %+v %v", out, err)
	}
	if _, total, _ := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{}); total != 0 {
		t.Fatalf("spam stored: %d", total)
	}
	for range FormPostsPerIP - 2 {
		if _, err := e.svc.SubmitForm(ctx, post(host, "a", "1")); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.svc.SubmitForm(ctx, post(host, "a", "1"))
	wantCode(t, err, domain.CodeRateLimited)
	if domain.AsError(err).RetryAfter <= 0 {
		t.Fatal("no retry hint")
	}
	other := post(host, "a", "1")
	other.IP = "203.0.113.6"
	if _, err := e.svc.SubmitForm(ctx, other); err != nil {
		t.Fatalf("other IP limited: %v", err)
	}
}

func TestFormChannelsOwnership(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann, bob := e.user("ann@pp.test"), e.user("bob@pp.test")
	v := mkFormSite(t, e, ann)
	res, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://hooks.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	id := res.Channel.ID
	calls := []func() error{
		func() error { _, err := e.svc.ListFormChannels(ctx, bob, v.Site.ID); return err },
		func() error {
			_, err := e.svc.AddFormChannel(ctx, bob, v.Site.ID, domain.ChannelWebhook, "https://x.example.com/")
			return err
		},
		func() error { _, err := e.svc.VerifyFormChannel(ctx, bob, v.Site.ID, id, "000000"); return err },
		func() error { _, err := e.svc.SetFormChannelEnabled(ctx, bob, v.Site.ID, id, false); return err },
		func() error { return e.svc.DeleteFormChannel(ctx, bob, v.Site.ID, id) },
		func() error { _, err := e.svc.RotateWebhookSecret(ctx, bob, v.Site.ID, id); return err },
		func() error { _, _, err := e.svc.ListFormSubmissions(ctx, bob, v.Site.ID, "", Page{}); return err },
		func() error {
			_, err := e.svc.ExportFormSubmissions(ctx, bob, v.Site.ID, "", &bytes.Buffer{})
			return err
		},
		func() error { _, err := e.svc.SetFormSiteSettings(ctx, bob, v.Site.ID, true); return err },
		func() error { _, err := e.svc.GetFormSettings(ctx, bob); return err },
	}
	for i, call := range calls {
		if err := call(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("call %d: %v", i, err)
		}
	}
	// The admin may manage any site's channels.
	if _, err := e.svc.ListFormChannels(ctx, e.admin, v.Site.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFormSettingsSecrets(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	bad := "123:short"
	_, err := e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{TelegramToken: &bad})
	wantCode(t, err, domain.CodeSettingInvalid)
	wrong := "999999:ABCdefGHIjklMNOpqrSTUvwxYZ012345"
	_, err = e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{TelegramToken: &wrong})
	wantCode(t, err, domain.CodeTelegramTokenFailed)

	tok, host, from, pass, sec := botToken, "smtp.example.com", "forms@example.com", "smtp-pass", "tls"
	port, days, difficulty := 465, 30, 2000
	st, err := e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{
		TelegramToken: &tok, SMTPHost: &host, SMTPPort: &port, SMTPFrom: &from, SMTPPassword: &pass,
		SMTPSecurity: &sec, RetentionDays: &days, AltchaMaxNumber: &difficulty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !st.TelegramTokenSet || st.TelegramBot != "dropsite_bot" || !st.SMTPPasswordSet || st.RetentionDays != 30 || st.AltchaMaxNumber != 2000 {
		t.Fatalf("settings %+v", st)
	}
	raw, _ := e.repo.GetSettings(ctx)
	for k, v := range raw {
		if strings.Contains(v, botToken) || strings.Contains(v, pass) {
			t.Fatalf("%s stored in plain text", k)
		}
	}
	rows, _, _ := e.svc.ListAudit(ctx, e.admin, AuditFilter{Action: "forms.settings.update"})
	b, _ := json.Marshal(rows)
	if len(rows) != 1 || bytes.Contains(b, []byte(botToken)) || bytes.Contains(b, []byte(pass)) {
		t.Fatalf("audit %s", b)
	}
	empty := ""
	st, err = e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{SMTPPassword: &empty, TelegramToken: &empty})
	if err != nil || st.SMTPPasswordSet || st.TelegramTokenSet || st.TelegramBot != "" {
		t.Fatalf("cleared %+v %v", st, err)
	}
	noFrom := ""
	_, err = e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{SMTPFrom: &noFrom})
	wantCode(t, err, domain.CodeSettingInvalid)
	zero := 0
	_, err = e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{RetentionDays: &zero})
	wantCode(t, err, domain.CodeSettingInvalid)
}

func configureChannels(t *testing.T, e *env) {
	t.Helper()
	tok, host, from := botToken, "smtp.example.com", "forms@example.com"
	port := 587
	if _, err := e.svc.UpdateFormSettings(context.Background(), e.admin, FormSettingsUpdate{
		TelegramToken: &tok, SMTPHost: &host, SMTPPort: &port, SMTPFrom: &from,
	}); err != nil {
		t.Fatal(err)
	}
}

func lastCode(t *testing.T, msg string) string {
	t.Helper()
	for _, w := range strings.FieldsFunc(msg, func(r rune) bool { return r < '0' || r > '9' }) {
		if len(w) == forms.DigitCodeLen {
			return w
		}
	}
	t.Fatalf("no code in %q", msg)
	return ""
}

func TestTelegramAndEmailVerification(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	_, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelTelegram, "42")
	wantCode(t, err, domain.CodeChannelUnavailable)
	configureChannels(t, e)

	tg, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelTelegram, "42")
	if err != nil || tg.Channel.Status != domain.ChannelPending || len(f.telegram) != 1 {
		t.Fatalf("telegram %+v %v", tg, err)
	}
	code := lastCode(t, f.telegram[0])
	for range FormCodeMaxAttempts - 1 {
		_, err = e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, tg.Channel.ID, "999999x")
		wantCode(t, err, domain.CodeChannelCodeInvalid)
	}
	ch, err := e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, tg.Channel.ID, code)
	if err != nil || ch.Status != domain.ChannelActive {
		t.Fatalf("verify %+v %v", ch, err)
	}

	em, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "sales@example.com")
	if err != nil || len(f.emails) != 1 {
		t.Fatalf("email %+v %v", em, err)
	}
	code = lastCode(t, f.emails[0])
	for range FormCodeMaxAttempts {
		_, _ = e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, em.Channel.ID, "000000x")
	}
	if _, err := e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, em.Channel.ID, code); err == nil {
		t.Fatal("code still valid after too many attempts")
	}
	em2, _ := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "ops@example.com")
	code = lastCode(t, f.emails[1])
	e.clock.Advance(FormCodeTTL + time.Second)
	_, err = e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, em2.Channel.ID, code)
	wantCode(t, err, domain.CodeChannelCodeInvalid)

	f.fail["telegram"] = []error{forms.Permanent("telegram: 400 chat not found")}
	_, err = e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelTelegram, "43")
	wantCode(t, err, domain.CodeChannelInvalid)
	_, err = e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "bad\r\nBcc: x@y.z")
	wantCode(t, err, domain.CodeChannelInvalid)
	_, err = e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://10.0.0.5/")
	wantCode(t, err, domain.CodeChannelInvalid)

	// Only the verified Telegram channel receives submissions.
	f.telegram, f.emails = nil, nil
	if _, err := e.svc.SubmitForm(ctx, post(v.Site.ID+"."+testBase, "name", "<b>Ann</b>")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DeliverDueForms(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.telegram) != 1 || !strings.HasPrefix(f.telegram[0], "42|") || !strings.Contains(f.telegram[0], "name: <b>Ann</b>") || len(f.emails) != 0 {
		t.Fatalf("telegram %v emails %v", f.telegram, f.emails)
	}
}

func TestTelegramDeepLinkBinding(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	configureChannels(t, e)
	res, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelTelegram, "")
	if err != nil || !strings.HasPrefix(res.Link, "https://t.me/dropsite_bot?start=") {
		t.Fatalf("link %+v %v", res, err)
	}
	code := strings.TrimPrefix(res.Link, "https://t.me/dropsite_bot?start=")
	f.updates = []forms.Update{
		{ID: 5, ChatID: "777", Text: "/start " + forms.LinkCode()},
		{ID: 6, ChatID: "4242", Text: "/start " + code},
	}
	if err := e.svc.PollTelegramLinks(ctx); err != nil {
		t.Fatal(err)
	}
	chs, _ := e.svc.ListFormChannels(ctx, ann, v.Site.ID)
	if len(chs) != 1 || chs[0].Target != "4242" || chs[0].Status != domain.ChannelActive {
		t.Fatalf("channels %+v", chs)
	}
	if e.svc.tgOffset != 7 || len(f.telegram) != 1 || !strings.HasPrefix(f.telegram[0], "4242|") {
		t.Fatalf("offset %d sent %v", e.svc.tgOffset, f.telegram)
	}
}

func TestDeliveryRetriesAndPermanentFailure(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	if _, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://hooks.example.com/"); err != nil {
		t.Fatal(err)
	}
	host := v.Site.ID + "." + testBase
	f.fail["webhook"] = []error{forms.Temporary("webhook: HTTP 503")}
	if _, err := e.svc.SubmitForm(ctx, post(host, "a", "1")); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.DeliverDueForms(ctx)
	subs, _, _ := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	d := subs[0].Deliveries[0]
	if d.Status != domain.DeliveryPending || d.Attempts != 1 || d.LastError != "webhook: HTTP 503" {
		t.Fatalf("after failure %+v", d)
	}
	if n, _ := e.svc.DeliverDueForms(ctx); n != 0 {
		t.Fatal("retried before the delay")
	}
	e.clock.Advance(retryDelay(1))
	_, _ = e.svc.DeliverDueForms(ctx)
	subs, _, _ = e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	if d := subs[0].Deliveries[0]; d.Status != domain.DeliverySent || d.Attempts != 2 || len(f.hooks) != 1 {
		t.Fatalf("after retry %+v", d)
	}

	// A permanent error stops at once; endless temporary errors stop after the limit.
	f.fail["webhook"] = []error{forms.Permanent("webhook: HTTP 404")}
	_, _ = e.svc.SubmitForm(ctx, post(host, "a", "2"))
	_, _ = e.svc.DeliverDueForms(ctx)
	errs := make([]error, formDeliveryMaxTries)
	for i := range errs {
		errs[i] = forms.Temporary("webhook: request failed")
	}
	f.fail["webhook"] = errs
	_, _ = e.svc.SubmitForm(ctx, post(host, "a", "3"))
	for range formDeliveryMaxTries {
		e.clock.Advance(retryDelay(10))
		_, _ = e.svc.DeliverDueForms(ctx)
	}
	subs, _, _ = e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	if d := subs[1].Deliveries[0]; d.Status != domain.DeliveryFailed || d.Attempts != 1 {
		t.Fatalf("permanent %+v", d)
	}
	if d := subs[0].Deliveries[0]; d.Status != domain.DeliveryFailed || d.Attempts != formDeliveryMaxTries {
		t.Fatalf("exhausted %+v", d)
	}
}

func TestRetryDelayGrows(t *testing.T) {
	if retryDelay(1) != 30*time.Second || retryDelay(2) != time.Minute || retryDelay(8) != 64*time.Minute {
		t.Fatal(retryDelay(1), retryDelay(8))
	}
}

func TestFormRetentionAndExport(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	_, _ = e.svc.SubmitForm(ctx, post(host, "name", "=cmd"))
	var buf bytes.Buffer
	if _, err := e.svc.ExportFormSubmissions(ctx, ann, v.Site.ID, "", &buf); err != nil || !strings.Contains(buf.String(), "'=cmd") {
		t.Fatalf("csv %q %v", buf.String(), err)
	}
	e.clock.Advance(DefaultFormRetentionDays*24*time.Hour + time.Hour)
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{}); total != 0 {
		t.Fatal("old submission kept")
	}
}

func solve(t *testing.T, ch altcha.Challenge) string {
	t.Helper()
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: time.Minute})
	if err != nil || sol == nil {
		t.Fatalf("solve %v", err)
	}
	b, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	return base64.StdEncoding.EncodeToString(b)
}

func TestAltchaRequiredByFlag(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	min := forms.MinAltchaMaxNumber
	if _, err := e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{AltchaMaxNumber: &min}); err != nil {
		t.Fatal(err)
	}
	// Flag off: the field is ignored.
	p := post(host, "a", "1")
	p.Data.Altcha = "garbage"
	if out, err := e.svc.SubmitForm(ctx, p); err != nil || !out.Stored {
		t.Fatalf("flag off %+v %v", out, err)
	}
	if _, err := e.svc.SetFormSiteSettings(ctx, ann, v.Site.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.SubmitForm(ctx, p)
	wantCode(t, err, domain.CodeFormCaptcha)

	ch, err := e.svc.AltchaChallenge(ctx, host, "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	good := post(host, "a", "1")
	good.IP = "198.51.100.7"
	good.Data.Altcha = solve(t, ch)
	if out, err := e.svc.SubmitForm(ctx, good); err != nil || !out.Stored {
		t.Fatalf("solved %+v %v", out, err)
	}
	_, err = e.svc.SubmitForm(ctx, good)
	wantCode(t, err, domain.CodeFormCaptcha) // replay

	other := mkFormSite(t, e, ann)
	if _, err := e.svc.SetFormSiteSettings(ctx, ann, other.Site.ID, true); err != nil {
		t.Fatal(err)
	}
	ch2, _ := e.svc.AltchaChallenge(ctx, host, "198.51.100.8")
	foreign := post(other.Site.ID+"."+testBase, "a", "1")
	foreign.Data.Altcha = solve(t, ch2)
	_, err = e.svc.SubmitForm(ctx, foreign)
	wantCode(t, err, domain.CodeFormCaptcha)

	if _, err := e.svc.AltchaChallenge(ctx, "nope."+testBase, "1.1.1.1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown host: %v", err)
	}
}

func TestAltchaEscalationPerIP(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	base := forms.MinAltchaMaxNumber
	if _, err := e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{AltchaMaxNumber: &base}); err != nil {
		t.Fatal(err)
	}
	// The counter lies in [max/2, max): the key prefix length does not show
	// it, so check the bound through the solution counter instead.
	maxOf := func(ip string) int {
		ch, err := e.svc.AltchaChallenge(ctx, host, ip)
		if err != nil {
			t.Fatal(err)
		}
		sol, _ := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: time.Minute})
		return sol.Counter
	}
	for level := range 3 {
		c := maxOf("192.0.2.1")
		lo, hi := forms.Difficulty(base, level)/2, forms.Difficulty(base, level)
		if c < lo || c >= hi {
			t.Fatalf("level %d: counter %d not in [%d,%d)", level, c, lo, hi)
		}
	}
	if c := maxOf("192.0.2.2"); c >= base {
		t.Fatalf("another IP escalated: %d", c)
	}
	e.clock.Advance(forms.EscalationWindow + time.Second)
	if c := maxOf("192.0.2.1"); c >= base {
		t.Fatalf("no decay: %d", c)
	}
	// A rejected post raises the level at once.
	hp := post(host, "a", "1")
	hp.IP = "192.0.2.3"
	hp.Data.Honeypot = true
	_, _ = e.svc.SubmitForm(ctx, hp)
	if c := maxOf("192.0.2.3"); c < base*forms.EscalationFactor/2 {
		t.Fatalf("penalty ignored: %d", c)
	}
}
