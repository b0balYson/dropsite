package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// into adapts a buffer to the start callback of ExportFormSubmissions.
func into(buf *bytes.Buffer) func(string) io.Writer {
	return func(string) io.Writer { return buf }
}

func submitFrom(t *testing.T, e *env, host string, i int, fields ...string) {
	t.Helper()
	p := post(host, fields...)
	p.IP = fmt.Sprintf("203.0.113.%d", i%250+1)
	if _, err := e.svc.SubmitForm(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func addHook(t *testing.T, e *env, owner Actor, site, target string) int64 {
	t.Helper()
	res, err := e.svc.AddFormChannel(context.Background(), owner, site, domain.ChannelWebhook, target)
	if err != nil {
		t.Fatal(err)
	}
	return res.Channel.ID
}

func TestExportCapsColumnsAndStreams(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	for i := range 40 {
		fields := []string{"email", fmt.Sprintf("u%d@x.test", i)}
		for j := range 10 {
			fields = append(fields, fmt.Sprintf("f%d_%d", i, j), "v")
		}
		submitFrom(t, e, host, i, fields...)
	}
	var buf bytes.Buffer
	var gotSite string
	err := e.svc.ExportFormSubmissions(ctx, ann, v.Site.ID, "", func(site string) io.Writer { gotSite = site; return &buf })
	if err != nil || gotSite != v.Site.ID {
		t.Fatal(err, gotSite)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 41 || len(rows[0]) != 3+forms.MaxCSVColumns+1 || rows[0][3] != "email" || rows[1][3] != "u39@x.test" {
		t.Fatalf("%d rows, %d columns, header %v", len(rows), len(rows[0]), rows[0][:4])
	}

	// Without access nothing is started; a size cap truncates at a row boundary.
	bob := e.user("bob@pp.test")
	started := false
	err = e.svc.ExportFormSubmissions(ctx, bob, v.Site.ID, "", func(string) io.Writer { started = true; return io.Discard })
	if err == nil || started {
		t.Fatalf("foreign export: started=%v err=%v", started, err)
	}
	old := formExportMaxBytes
	formExportMaxBytes = 1
	t.Cleanup(func() { formExportMaxBytes = old })
	buf.Reset()
	if err := e.svc.ExportFormSubmissions(ctx, ann, v.Site.ID, "", into(&buf)); err != nil {
		t.Fatal(err)
	}
	if rows, _ := csv.NewReader(&buf).ReadAll(); len(rows) >= 41 {
		t.Fatalf("size cap ignored: %d rows", len(rows))
	}
}

// slowSender blocks webhooks to targets containing "slow" until released.
type slowSender struct {
	*fakeSender
	release  chan struct{}
	mu       sync.Mutex
	inflight map[string]int
	maxSlow  int
	maxPer   map[string]int
	fast     chan string
}

func (s *slowSender) Webhook(ctx context.Context, target, secret string, body []byte) error {
	if !strings.Contains(target, "slow") {
		s.fast <- target
		return nil
	}
	s.mu.Lock()
	s.inflight[target]++
	total := 0
	for _, n := range s.inflight {
		total += n
	}
	s.maxSlow = max(s.maxSlow, total)
	s.maxPer[target] = max(s.maxPer[target], s.inflight[target])
	s.mu.Unlock()
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	s.mu.Lock()
	s.inflight[target]--
	s.mu.Unlock()
	return forms.Temporary("webhook: request failed")
}

func TestSlowSiteDoesNotStallOthers(t *testing.T) {
	e, f := formEnv(t)
	slow := &slowSender{fakeSender: f, release: make(chan struct{}), inflight: map[string]int{},
		maxPer: map[string]int{}, fast: make(chan string, 4)}
	e.svc.senders = slow
	ann, bob := e.user("ann@pp.test"), e.user("bob@pp.test")
	noisy, quiet := mkFormSite(t, e, ann), mkFormSite(t, e, bob)
	for i := range 4 {
		addHook(t, e, ann, noisy.Site.ID, fmt.Sprintf("https://slow%d.example.com/", i))
	}
	addHook(t, e, bob, quiet.Site.ID, "https://fast.example.com/")
	for i := range 30 {
		submitFrom(t, e, noisy.Site.ID+"."+testBase, i, "n", fmt.Sprint(i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.RunFormDelivery(ctx); close(done) }()
	defer func() { close(slow.release); cancel(); <-done }()

	// The quiet site's submission arrives while the noisy site's receivers hang.
	time.Sleep(50 * time.Millisecond)
	submitFrom(t, e, quiet.Site.ID+"."+testBase, 1, "n", "quiet")
	select {
	case got := <-slow.fast:
		if got != "https://fast.example.com/" {
			t.Fatal(got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the quiet site was stalled by the slow one")
	}
	slow.mu.Lock()
	defer slow.mu.Unlock()
	if slow.maxSlow > formDeliveryPerSite {
		t.Fatalf("noisy site had %d attempts in flight", slow.maxSlow)
	}
	for target, n := range slow.maxPer {
		if n > 1 {
			t.Fatalf("%s had %d attempts in flight", target, n)
		}
	}
}

func TestCircuitBreakerPausesWebhookUntilResumed(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase
	id := addHook(t, e, ann, v.Site.ID, "https://down.example.com/")
	for i := range formChannelPauseAfter + 1 {
		submitFrom(t, e, host, i, "n", fmt.Sprint(i))
	}
	for range formChannelPauseAfter {
		f.fail["webhook"] = []error{forms.Temporary("webhook: HTTP 503")}
		if n, err := e.svc.DeliverDueForms(ctx); err != nil || n != 1 {
			t.Fatalf("deliver %d %v", n, err)
		}
	}
	chs, _ := e.svc.ListFormChannels(ctx, ann, v.Site.ID)
	if chs[0].PausedAt == nil || chs[0].ConsecutiveFailures != formChannelPauseAfter {
		t.Fatalf("channel not paused: %+v", chs[0])
	}
	if rows, _, _ := e.svc.ListAudit(ctx, e.admin, AuditFilter{Action: "form.channel.pause"}); len(rows) != 1 {
		t.Fatalf("pause audit %d", len(rows))
	}
	e.clock.Advance(24 * time.Hour)
	if n, _ := e.svc.DeliverDueForms(ctx); n != 0 {
		t.Fatal("paused channel still delivers")
	}
	ch, err := e.svc.SetFormChannelEnabled(ctx, ann, v.Site.ID, id, true)
	if err != nil || ch.PausedAt != nil {
		t.Fatalf("resume %+v %v", ch, err)
	}
	if n, _ := e.svc.DeliverDueForms(ctx); n != 1 || len(f.hooks) != 1 {
		t.Fatalf("resumed channel: %d attempts, %d hooks", n, len(f.hooks))
	}
}

func TestTurnedOffChannelCancelsQueue(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	id := addHook(t, e, ann, v.Site.ID, "https://hooks.example.com/")
	submitFrom(t, e, v.Site.ID+"."+testBase, 1, "n", "1")
	if _, err := e.svc.SetFormChannelEnabled(ctx, ann, v.Site.ID, id, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.DeliverDueForms(ctx); n != 0 || len(f.hooks) != 0 {
		t.Fatal("delivered to a turned-off channel")
	}
	subs, _, _ := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	if d := subs[0].Deliveries[0]; d.Status != domain.DeliveryCancelled {
		t.Fatalf("delivery %+v", d)
	}
}

func TestConfirmationMessagesAreLimited(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	configureChannels(t, e)
	for range FormConfirmPerRecipient {
		if _, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "victim@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "VICTIM@example.com")
	wantCode(t, err, domain.ErrRateLimited.Code)
	if len(f.emails) != FormConfirmPerRecipient {
		t.Fatalf("sent %d", len(f.emails))
	}
	// The subject is neutral; the site address is only in the body.
	parts := strings.SplitN(f.emails[0], "|", 3)
	if parts[1] != confirmSubject || strings.Contains(parts[1], v.Site.ID) || !strings.Contains(parts[2], v.Site.ID) {
		t.Fatalf("e-mail %q", f.emails[0])
	}
	if _, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelTelegram, "42"); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmLimitsInstallationWide(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	c := newConfirmLimits(clock.Now)
	for i := range FormConfirmPerHour {
		if err := c.allow(fmt.Sprintf("email:a%d@x.test", i)); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := c.allow("email:new@x.test"); err == nil {
		t.Fatal("installation cap ignored")
	}
	clock.Advance(time.Hour + time.Second)
	if err := c.allow("email:new@x.test"); err != nil {
		t.Fatal("the refused attempt must not use the recipient's quota", err)
	}
}

func TestSMTPErrorsStayInTheLog(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	configureChannels(t, e)
	f.fail["email"] = []error{&forms.SendError{Permanent: true, Msg: "e-mail: refused", Detail: "smtp: 550 relay.internal.lan denied"}}
	_, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "sales@example.com")
	wantCode(t, err, domain.CodeChannelUnavailable)
	if strings.Contains(err.Error(), "550") || strings.Contains(err.Error(), "internal") {
		t.Fatalf("user sees SMTP internals: %v", err)
	}
}

func TestVerifyAttemptsAreAtomic(t *testing.T) {
	e, f := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	configureChannels(t, e)
	res, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelEmail, "sales@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code := lastCode(t, f.emails[0])
	var wg sync.WaitGroup
	for range 4 * FormCodeMaxAttempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, res.Channel.ID, "000000x")
		}()
	}
	wg.Wait()
	_, err = e.svc.VerifyFormChannel(ctx, ann, v.Site.ID, res.Channel.ID, code)
	wantCode(t, err, domain.CodeChannelCodeInvalid)
	chs, _ := e.svc.ListFormChannels(ctx, ann, v.Site.ID)
	if chs[0].VerifyAttempts != FormCodeMaxAttempts || chs[0].Status != domain.ChannelPending {
		t.Fatalf("channel %+v", chs[0])
	}
}
