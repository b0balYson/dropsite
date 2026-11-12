package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// nopSender accepts every delivery.
type nopSender struct{ codes []string }

func (n *nopSender) TelegramMe(context.Context, string) (string, error) { return "test_bot", nil }
func (n *nopSender) TelegramSend(_ context.Context, _, _, text string) error {
	n.codes = append(n.codes, text)
	return nil
}
func (n *nopSender) TelegramUpdates(context.Context, string, int64) ([]forms.Update, error) {
	return nil, nil
}
func (n *nopSender) Email(_ context.Context, _ forms.SMTPConfig, _, _, body string) error {
	n.codes = append(n.codes, body)
	return nil
}
func (n *nopSender) Webhook(context.Context, string, string, []byte) error { return nil }

func submitTo(t *testing.T, h *harness, siteID string, fields ...string) {
	t.Helper()
	p := service.FormPost{Host: siteID + "." + testenv.BaseDomain, Form: "lead", IP: "198.51.100.1"}
	for i := 0; i+1 < len(fields); i += 2 {
		p.Data.Fields = append(p.Data.Fields, domain.FormField{Name: fields[i], Value: fields[i+1]})
	}
	if _, err := h.env.Svc.SubmitForm(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestSitePageShowsSubmissionsEscaped(t *testing.T) {
	h := newHarness(t, testenv.WithFormSender(&nopSender{}))
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	empty := h.get("/sites/" + v.Site.ID)
	wantContains(t, empty.body, "Заявки", "Заявок пока нет", "/_form/lead")

	submitTo(t, h, v.Site.ID, "name", `<script>alert(1)</script>`, "msg", "line1\nline2")
	page := h.get("/sites/" + v.Site.ID)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page.body, "&lt;script&gt;alert(1)&lt;/script&gt;", "line1\nline2", "Каналы не подключены")
	wantNotContains(t, page.body, "<script>alert(1)</script>")

	forms := h.get("/sites/" + v.Site.ID + "/forms")
	wantStatus(t, forms, http.StatusOK)
	wantContains(t, forms.body, "&lt;script&gt;", "Скачать CSV", "Подключить вебхук", "Telegram-бот не настроен")
	wantNotContains(t, forms.body, "<script>alert(1)</script>")

	csv := h.get("/sites/" + v.Site.ID + "/forms/export.csv")
	wantStatus(t, csv, http.StatusOK)
	if csv.resp.Header.Get("Content-Type") != "text/csv; charset=utf-8" || !strings.Contains(csv.body, "<script>") {
		t.Fatalf("csv %v %q", csv.resp.Header, csv.body)
	}
}

func TestFormsPageForeignSite(t *testing.T) {
	h := newHarness(t, testenv.WithFormSender(&nopSender{}))
	bob := h.env.User("bob@pp.test")
	v, err := h.env.Svc.CreateSite(context.Background(), bob, "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	h.loginUser("alice@pp.test")
	for _, path := range []string{"/sites/" + v.Site.ID + "/forms", "/sites/" + v.Site.ID + "/forms/export.csv"} {
		wantStatus(t, h.get(path), http.StatusNotFound)
	}
	p := h.submit("/sites", "/sites/"+v.Site.ID+"/forms/channels", url.Values{"kind": {"webhook"}, "target": {"https://h.example.com/"}})
	wantStatus(t, p, http.StatusNotFound)
}

func TestWebhookChannelSecretShownOnce(t *testing.T) {
	h := newHarness(t, testenv.WithFormSender(&nopSender{}))
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	base := "/sites/" + v.Site.ID + "/forms"
	p := h.submit(base, base+"/channels", url.Values{"kind": {"webhook"}, "target": {"https://hooks.example.com/x"}})
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, "whsec_", "Секрет подписи вебхука")
	secret := p.body[strings.Index(p.body, "whsec_"):]
	secret = secret[:strings.IndexAny(secret, "<\n")]
	again := h.get(base)
	wantNotContains(t, again.body, secret)
	wantContains(t, again.body, "работает")

	bad := h.submit(base, base+"/channels", url.Values{"kind": {"webhook"}, "target": {"https://127.0.0.1/"}})
	wantStatus(t, bad, http.StatusUnprocessableEntity)
	wantContains(t, bad.body, "Канал не добавлен", `value="https://127.0.0.1/"`)

	toggle := h.submit(base, base+"/channels/1/toggle", url.Values{"enabled": {"0"}})
	wantStatus(t, toggle, http.StatusSeeOther)
	wantContains(t, h.follow(toggle).body, "Канал выключен")

	altcha := h.submit(base, base+"/settings", url.Values{"require_altcha": {"1"}, "back": {"forms"}})
	if altcha.location() != base {
		t.Fatalf("back to %q", altcha.location())
	}
	st, _ := h.env.Svc.GetFormSiteSettings(context.Background(), alice, v.Site.ID)
	if !st.RequireAltcha {
		t.Fatal("altcha flag not saved")
	}
}

func TestEmailChannelVerification(t *testing.T) {
	sender := &nopSender{}
	h := newHarness(t, testenv.WithFormSender(sender))
	host, from := "smtp.example.com", "forms@example.com"
	port := 587
	if _, err := h.env.Svc.UpdateFormSettings(context.Background(), h.env.Admin, service.FormSettingsUpdate{
		SMTPHost: &host, SMTPPort: &port, SMTPFrom: &from,
	}); err != nil {
		t.Fatal(err)
	}
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	base := "/sites/" + v.Site.ID + "/forms"
	p := h.submit(base, base+"/channels", url.Values{"kind": {"email"}, "target": {"sales@example.com"}})
	wantStatus(t, p, http.StatusSeeOther)
	page := h.follow(p)
	wantContains(t, page.body, "Код отправлен", "Код подтверждения")
	code := sender.codes[0][strings.Index(sender.codes[0], ": ")+2:][:6]
	ok := h.submit(base, base+"/channels/1/verify", url.Values{"code": {code}})
	wantStatus(t, ok, http.StatusSeeOther)
	wantContains(t, h.follow(ok).body, "Канал подтверждён")
}

func TestAdminFormSettingsHideSecrets(t *testing.T) {
	h := newHarness(t, testenv.WithFormSender(&nopSender{}))
	h.loginAdmin()
	form := url.Values{
		"telegram_token": {"123456:ABCdefGHIjklMNOpqrSTUvwxYZ012345"}, "smtp_host": {"smtp.example.com"},
		"smtp_port": {"465"}, "smtp_security": {"tls"}, "smtp_from": {"forms@example.com"},
		"smtp_username": {"robot"}, "smtp_password": {"very-secret-pass"}, "retention_days": {"30"}, "altcha_max_number": {"5000"},
	}
	p := h.submit("/admin/settings", "/admin/settings/forms", form)
	wantStatus(t, p, http.StatusSeeOther)
	page := h.follow(p)
	wantContains(t, page.body, "Настройки заявок сохранены", "@test_bot", "smtp.example.com", `value="30"`)
	wantNotContains(t, page.body, "very-secret-pass", "ABCdefGHIjklMNOpqrSTUvwxYZ012345")

	// Saving again with empty secret fields keeps the secrets.
	form.Set("telegram_token", "")
	form.Set("smtp_password", "")
	wantStatus(t, h.submit("/admin/settings", "/admin/settings/forms", form), http.StatusSeeOther)
	st, _ := h.env.Svc.GetFormSettings(context.Background(), h.env.Admin)
	if !st.TelegramTokenSet || !st.SMTPPasswordSet {
		t.Fatalf("secrets lost: %+v", st)
	}
	form.Set("clear_smtp_password", "1")
	form.Set("retention_days", "x")
	bad := h.submit("/admin/settings", "/admin/settings/forms", form)
	wantContains(t, h.follow(bad).body, "должно быть числом")
}

func TestFormsPagesNeedLogin(t *testing.T) {
	h := newHarness(t)
	p := h.get("/sites/abc/forms")
	if p.resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(p.location(), "/login") {
		t.Fatalf("anonymous: %d %q", p.resp.StatusCode, p.location())
	}
	h.loginUser("alice@pp.test")
	wantStatus(t, h.post("/admin/settings/forms", url.Values{}), http.StatusNotFound)
}
