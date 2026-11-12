package remote

import (
	"context"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

const testBotToken = "123456:ABCdefGHIjklMNOpqrSTUvwxYZ012345"

// sender records confirmation messages and webhook deliveries.
type sender struct {
	mu       sync.Mutex
	messages []string
	hooks    int
}

func (s *sender) TelegramMe(context.Context, string) (string, error) { return "dropsite_bot", nil }

func (s *sender) TelegramSend(_ context.Context, _, chatID, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, chatID+"|"+text)
	return nil
}

func (s *sender) TelegramUpdates(context.Context, string, int64) ([]forms.Update, error) {
	return nil, nil
}

func (s *sender) Email(_ context.Context, _ forms.SMTPConfig, to, _, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, to+"|"+body)
	return nil
}

func (s *sender) Webhook(context.Context, string, string, []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks++
	return nil
}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

func (s *sender) lastCode(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		t.Fatal("no message sent")
	}
	code := codeRe.FindString(s.messages[len(s.messages)-1])
	if code == "" {
		t.Fatalf("no code in %q", s.messages[len(s.messages)-1])
	}
	return code
}

func newFormsRig(t *testing.T, configure bool) (*rig, *sender) {
	t.Helper()
	snd := &sender{}
	env := testenv.New(t, testenv.WithFormSender(snd))
	if configure {
		token, host, port, from := testBotToken, "smtp.example.com", 587, "forms@example.com"
		if _, err := env.Svc.UpdateFormSettings(context.Background(), env.Admin, service.FormSettingsUpdate{
			TelegramToken: &token, SMTPHost: &host, SMTPPort: &port, SMTPFrom: &from,
		}); err != nil {
			t.Fatal(err)
		}
	}
	r := &rig{t: t, env: env}
	r.srv = httptest.NewServer(New(env.Svc, Options{}))
	t.Cleanup(r.srv.Close)
	return r, snd
}

func submit(t *testing.T, env *testenv.Env, siteID, form string, i int, fields ...string) {
	t.Helper()
	p := service.FormPost{Host: siteID + "." + env.Config.BaseDomain, Form: form, IP: fmt.Sprintf("198.51.100.%d", i)}
	for j := 0; j+1 < len(fields); j += 2 {
		p.Data.Fields = append(p.Data.Fields, domain.FormField{Name: fields[j], Value: fields[j+1]})
	}
	if _, err := env.Svc.SubmitForm(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteFormsSettingsAndSubmissions(t *testing.T) {
	r, snd := newFormsRig(t, true)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	cs := r.session(r.env.Token(ann, "mcp"))
	v, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe")
	if err != nil {
		t.Fatal(err)
	}

	out, msg := call(t, cs, "get_form_settings", map[string]any{"site": "cafe"})
	if msg != "" || out["require_altcha"] != false || out["telegram_available"] != true || out["telegram_bot"] != "@dropsite_bot" ||
		out["email_available"] != true || !strings.HasSuffix(out["form_endpoint"].(string), "/_form/<name>") ||
		!strings.Contains(out["form_endpoint"].(string), "cafe.") || !strings.Contains(out["form_snippet"].(string), "altcha-widget") ||
		!strings.HasSuffix(out["cabinet_url"].(string), "/sites/"+v.Site.ID+"/forms") {
		t.Fatalf("settings: %v %q", out, msg)
	}
	out, _ = call(t, cs, "set_form_settings", map[string]any{"site": "cafe", "require_altcha": true})
	if out["require_altcha"] != true {
		t.Fatalf("set: %v", out)
	}
	call(t, cs, "set_form_settings", map[string]any{"site": "cafe", "require_altcha": false})
	audit, _, _ := r.env.Svc.ListAudit(ctx, r.env.Admin, service.AuditFilter{Action: "form.settings.update"})
	if len(audit) != 2 {
		t.Fatalf("audit: %v", audit)
	}

	hook, msg := call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "webhook", "target": "https://hooks.example.com/in"})
	if msg != "" || hook["secret"] == "" || !strings.Contains(hook["next_step"].(string), "only this once") {
		t.Fatalf("webhook: %v %q", hook, msg)
	}
	submit(t, r.env, v.Site.ID, "lead", 1, "name", "Anna", "phone", "=1+1")
	r.env.Clock.Advance(2 * 24 * time.Hour)
	submit(t, r.env, v.Site.ID, "order", 2, "name", "Boris")
	submit(t, r.env, v.Site.ID, "lead", 3, "name", "Clara")
	for range 3 {
		if n, err := r.env.Svc.DeliverDueForms(ctx); err != nil || n != 1 {
			t.Fatalf("deliver %d %v", n, err)
		}
	}
	if snd.hooks != 3 {
		t.Fatalf("hooks %d", snd.hooks)
	}

	out, msg = call(t, cs, "list_submissions", map[string]any{"site": "cafe", "limit": 2})
	subs := out["submissions"].([]any)
	if msg != "" || out["total"] != float64(3) || len(subs) != 2 || out["next_cursor"] == "" || out["server_time"] == "" {
		t.Fatalf("page 1: %v %q", out, msg)
	}
	first := subs[0].(map[string]any)
	dl := first["deliveries"].([]any)
	if first["fields"].([]any)[0].(map[string]any)["value"] != "Clara" || len(dl) != 1 ||
		!strings.HasPrefix(dl[0].(map[string]any)["summary"].(string), "delivered at") {
		t.Fatalf("first: %v", first)
	}
	out, _ = call(t, cs, "list_submissions", map[string]any{"site": "cafe", "limit": 2, "cursor": out["next_cursor"]})
	if subs := out["submissions"].([]any); len(subs) != 1 || out["next_cursor"] != nil {
		t.Fatalf("page 2: %v", out)
	}
	out, _ = call(t, cs, "list_submissions", map[string]any{"site": "cafe", "days": 1})
	if out["total"] != float64(2) {
		t.Fatalf("days: %v", out)
	}
	out, _ = call(t, cs, "list_submissions", map[string]any{"site": "cafe", "query": "anna", "form": "LEAD"})
	if out["total"] != float64(1) {
		t.Fatalf("query: %v", out)
	}
	if _, msg := call(t, cs, "list_submissions", map[string]any{"site": "cafe", "cursor": "zzz"}); !strings.Contains(msg, "invalid_input") {
		t.Fatalf("bad cursor: %q", msg)
	}

	out, msg = call(t, cs, "export_submissions", map[string]any{"site": "cafe"})
	if msg != "" || out["rows"] != float64(3) || out["truncated"] != false || !strings.Contains(out["csv"].(string), "'=1+1") {
		t.Fatalf("export: %v %q", out, msg)
	}
	out, _ = call(t, cs, "export_submissions", map[string]any{"site": "cafe", "max_rows": 1})
	if out["rows"] != float64(1) || out["truncated"] != true || !strings.Contains(out["summary"].(string), out["cabinet_url"].(string)) {
		t.Fatalf("truncated export: %v", out)
	}

	// Another user sees nothing of this site.
	bob := r.env.User("bob@pp.test")
	bs := r.session(r.env.Token(bob, "mcp"))
	channelID := hook["channel"].(map[string]any)["id"].(string)
	for name, args := range map[string]map[string]any{
		"get_form_settings":     {"site": v.Site.ID},
		"set_form_settings":     {"site": v.Site.ID, "require_altcha": true},
		"list_submissions":      {"site": v.Site.ID},
		"export_submissions":    {"site": v.Site.ID},
		"list_channels":         {"site": v.Site.ID},
		"add_channel":           {"site": v.Site.ID, "kind": "webhook", "target": "https://hooks.example.com/b"},
		"verify_channel":        {"site": v.Site.ID, "channel_id": channelID, "code": "123456"},
		"set_channel_enabled":   {"site": v.Site.ID, "channel_id": channelID, "enabled": false},
		"delete_channel":        {"site": v.Site.ID, "channel_id": channelID, "confirm": channelID},
		"rotate_channel_secret": {"site": v.Site.ID, "channel_id": channelID, "confirm": channelID},
	} {
		if _, msg := call(t, bs, name, args); !strings.Contains(msg, "not found") {
			t.Errorf("foreign %s: %q", name, msg)
		}
	}
}

func TestRemoteFormsChannels(t *testing.T) {
	r, snd := newFormsRig(t, true)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	cs := r.session(r.env.Token(ann, "mcp"))
	if _, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe"); err != nil {
		t.Fatal(err)
	}

	tg, msg := call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "telegram"})
	if msg != "" || !strings.HasPrefix(tg["link"].(string), "https://t.me/dropsite_bot?start=") ||
		!strings.Contains(tg["next_step"].(string), "press Start") ||
		tg["channel"].(map[string]any)["state"] != "waiting_for_confirmation" {
		t.Fatalf("telegram link: %v %q", tg, msg)
	}
	em, msg := call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "email", "target": "ann@example.com"})
	if msg != "" || !strings.Contains(em["next_step"].(string), "verify_channel") {
		t.Fatalf("email: %v %q", em, msg)
	}
	emID := em["channel"].(map[string]any)["id"].(string)
	if _, msg := call(t, cs, "verify_channel", map[string]any{"site": "cafe", "channel_id": emID, "code": "000000"}); !strings.Contains(msg, "channel_code_invalid") {
		t.Fatalf("wrong code: %q", msg)
	}
	ch, msg := call(t, cs, "verify_channel", map[string]any{"site": "cafe", "channel_id": emID, "code": snd.lastCode(t)})
	if msg != "" || ch["state"] != "active" {
		t.Fatalf("verify: %v %q", ch, msg)
	}
	chat, msg := call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "telegram", "target": "123456789"})
	if msg != "" || !strings.Contains(chat["next_step"].(string), "123456789") {
		t.Fatalf("chat id: %v %q", chat, msg)
	}

	ch, _ = call(t, cs, "set_channel_enabled", map[string]any{"site": "cafe", "channel_id": emID, "enabled": false})
	if ch["state"] != "off" {
		t.Fatalf("off: %v", ch)
	}
	list, _ := call(t, cs, "list_channels", map[string]any{"site": "cafe"})
	if chs := list["channels"].([]any); len(chs) != 3 || list["telegram_available"] != true {
		t.Fatalf("list: %v", list)
	}
	hook, _ := call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "webhook", "target": "https://hooks.example.com/in"})
	hookID := hook["channel"].(map[string]any)["id"].(string)
	if _, msg := call(t, cs, "rotate_channel_secret", map[string]any{"site": "cafe", "channel_id": hookID, "confirm": "x"}); !strings.Contains(msg, "confirm") {
		t.Fatalf("unconfirmed rotate: %q", msg)
	}
	rot, msg := call(t, cs, "rotate_channel_secret", map[string]any{"site": "cafe", "channel_id": hookID, "confirm": "https://hooks.example.com/in"})
	if msg != "" || rot["secret"] == "" || rot["secret"] == hook["secret"] {
		t.Fatalf("rotate: %v %q", rot, msg)
	}
	if _, msg := call(t, cs, "rotate_channel_secret", map[string]any{"site": "cafe", "channel_id": emID, "confirm": emID}); !strings.Contains(msg, "channel_invalid") {
		t.Fatalf("rotate e-mail: %q", msg)
	}
	if _, msg := call(t, cs, "delete_channel", map[string]any{"site": "cafe", "channel_id": emID, "confirm": ""}); !strings.Contains(msg, "confirm") {
		t.Fatalf("unconfirmed delete: %q", msg)
	}
	out, msg := call(t, cs, "delete_channel", map[string]any{"site": "cafe", "channel_id": emID, "confirm": "ann@example.com"})
	if msg != "" || out["deleted"] != emID {
		t.Fatalf("delete: %v %q", out, msg)
	}
	if _, msg := call(t, cs, "verify_channel", map[string]any{"site": "cafe", "channel_id": "abc", "code": "1"}); !strings.Contains(msg, "list_channels") {
		t.Fatalf("bad id: %q", msg)
	}
	audit, _, _ := r.env.Svc.ListAudit(ctx, r.env.Admin, service.AuditFilter{Action: "form.channel.delete"})
	if len(audit) != 1 {
		t.Fatalf("audit: %v", audit)
	}
}

func TestRemoteFormsLimits(t *testing.T) {
	r, _ := newFormsRig(t, true)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	cs := r.session(r.env.Token(ann, "mcp"))
	if _, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe"); err != nil {
		t.Fatal(err)
	}
	// Confirmation codes per recipient are limited as in the web interface.
	var msg string
	for range 4 {
		_, msg = call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "email", "target": "x@example.com"})
	}
	if !strings.Contains(msg, "retry in") {
		t.Fatalf("codes per recipient: %q", msg)
	}
	// So is the number of channels a user may create per hour.
	for i := range service.FormChannelsPerHour {
		_, msg = call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": "webhook",
			"target": fmt.Sprintf("https://hooks.example.com/%d", i)})
	}
	if !strings.Contains(msg, "retry in") && !strings.Contains(msg, "channel_limit") {
		t.Fatalf("channels per hour: %q", msg)
	}
}

func TestRemoteFormsUnconfigured(t *testing.T) {
	r, _ := newFormsRig(t, false)
	ann := r.env.User("ann@pp.test")
	cs := r.session(r.env.Token(ann, "mcp"))
	if _, err := r.env.Svc.CreateSite(context.Background(), ann, "Cafe", "cafe"); err != nil {
		t.Fatal(err)
	}
	out, _ := call(t, cs, "get_form_settings", map[string]any{"site": "cafe"})
	if out["telegram_available"] != false || out["email_available"] != false || out["telegram_bot"] != nil {
		t.Fatalf("availability: %v", out)
	}
	for kind, want := range map[string]string{"telegram": "administrator", "email": "administrator"} {
		if _, msg := call(t, cs, "add_channel", map[string]any{"site": "cafe", "kind": kind, "target": "a@example.com"}); !strings.Contains(msg, want) ||
			!strings.Contains(msg, "channel_unavailable") {
			t.Errorf("%s: %q", kind, msg)
		}
	}
}
