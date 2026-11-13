package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// hookRecorder is a FormSender that records webhooks and refuses the rest.
type hookRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (h *hookRecorder) TelegramMe(context.Context, string) (string, error) {
	return "", forms.Permanent("no telegram in tests")
}
func (h *hookRecorder) TelegramSend(context.Context, string, string, string) error { return nil }
func (h *hookRecorder) TelegramUpdates(context.Context, string, int64) ([]forms.Update, error) {
	return nil, nil
}
func (h *hookRecorder) Email(context.Context, forms.SMTPConfig, string, string, string) error {
	return nil
}
func (h *hookRecorder) Webhook(_ context.Context, _, _ string, body []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bodies = append(h.bodies, string(body))
	return nil
}

type formHarness struct {
	*harness
	hooks *hookRecorder
	owner string
	site  siteDTO
	host  string
}

func newFormHarness(t *testing.T) *formHarness {
	t.Helper()
	rec := &hookRecorder{}
	h := newHarness(t, testenv.WithFormSender(rec))
	_, owner := h.user("owner@pp.test")
	site := h.createSite(owner, "Landing", "")
	return &formHarness{harness: h, hooks: rec, owner: owner, site: site, host: site.ID + "." + testenv.BaseDomain}
}

// visitor posts to the site host like a browser or fetch would.
func (f *formHarness) visitor(method, path, ctype, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = f.host
	req.RemoteAddr = "198.51.100.20:5000"
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

const formCT = "application/x-www-form-urlencoded"

func browser() reqOpt { return withHeader("Sec-Fetch-Mode", "navigate") }

func TestFormPostBrowserAndFetch(t *testing.T) {
	f := newFormHarness(t)
	rec := f.visitor(http.MethodPost, "/_form/lead", formCT, "name=Ann&_next=%2Fthanks.html", browser())
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/thanks.html" {
		t.Fatalf("redirect %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "name=Ann&_next=%2F%2Fevil.example%2F", browser(),
		withHeader("Accept-Language", "ru-RU,ru;q=0.9"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Спасибо") || rec.Header().Get("Location") != "" {
		t.Fatalf("thank-you page %d %s", rec.Code, rec.Body.String())
	}
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "name=Li", browser(),
		withHeader("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8"))
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `<html lang="zh-CN">`) || !strings.Contains(body, "谢谢") {
		t.Fatalf("zh thank-you page %d %s", rec.Code, body)
	}
	rec = f.visitor(http.MethodPost, "/_form/lead", "application/json", `{"name":"<script>alert(1)</script>"}`,
		withHeader("Accept", "application/json"))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("json %d %s", rec.Code, rec.Body.String())
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "Bob")
	fw, _ := mw.CreateFormFile("file", "a.pdf")
	_, _ = fw.Write([]byte("pdf"))
	_ = mw.Close()
	rec = f.visitor(http.MethodPost, "/_form/Call", mw.FormDataContentType(), buf.String())
	wantStatus(t, rec, http.StatusOK)

	subs := decodeAs[list[submissionDTO]](t, f.do(http.MethodGet, "/api/v1/sites/"+f.site.ID+"/forms/submissions", f.owner, nil))
	if subs.Total != 5 || subs.Items[0].Form != "call" || len(subs.Items[0].Fields) != 1 || subs.Items[1].Fields[0].Value != "<script>alert(1)</script>" {
		t.Fatalf("submissions %+v", subs)
	}
	filtered := decodeAs[list[submissionDTO]](t, f.do(http.MethodGet, "/api/v1/sites/"+f.site.ID+"/forms/submissions?form=lead", f.owner, nil))
	if filtered.Total != 4 {
		t.Fatalf("filter %d", filtered.Total)
	}
}

func TestFormPostErrors(t *testing.T) {
	f := newFormHarness(t)
	rec := f.visitor(http.MethodGet, "/_form/lead", "", "")
	wantStatus(t, rec, http.StatusNotFound)

	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "a="+strings.Repeat("x", forms.MaxBodyBytes))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"form_too_large"`) {
		t.Fatalf("large %d %s", rec.Code, rec.Body.String())
	}
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "a=1", withHeader("Origin", "https://evil.example"))
	wantStatus(t, rec, http.StatusForbidden)
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "a=1", withHeader("Sec-Fetch-Site", "cross-site"))
	wantStatus(t, rec, http.StatusForbidden)
	rec = f.visitor(http.MethodPost, "/_form/lead", "text/plain", "a=1")
	wantStatus(t, rec, http.StatusUnsupportedMediaType)
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "_next=/x", browser())
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("empty form %d %s", rec.Code, rec.Body.String())
	}
	rec = f.visitor(http.MethodPost, "/_form/a%2Fb", formCT, "a=1")
	wantStatus(t, rec, http.StatusNotFound)
	other := httptest.NewRequest(http.MethodPost, "/_form/lead", strings.NewReader("a=1"))
	other.Host = "unknown." + testenv.BaseDomain
	other.Header.Set("Content-Type", formCT)
	orec := httptest.NewRecorder()
	f.h.ServeHTTP(orec, other)
	wantStatus(t, orec, http.StatusNotFound)

	// The honeypot looks like success and stores nothing.
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "name=x&_hp=spam", withHeader("Accept", "application/json"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("honeypot %d %s", rec.Code, rec.Body.String())
	}
	subs := decodeAs[list[submissionDTO]](t, f.do(http.MethodGet, "/api/v1/sites/"+f.site.ID+"/forms/submissions", f.owner, nil))
	if subs.Total != 0 {
		t.Fatalf("stored %d", subs.Total)
	}
	for i := 0; ; i++ {
		rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "a=1")
		if rec.Code == http.StatusTooManyRequests {
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("no Retry-After")
			}
			break
		}
		if i > 50 {
			t.Fatal("no rate limit")
		}
	}
}

func TestAltchaEndpoints(t *testing.T) {
	f := newFormHarness(t)
	rec := f.visitor(http.MethodGet, "/_dropsite/altcha.js", "", "")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" ||
		rec.Header().Get("Cache-Control") != "public, max-age=3600" || rec.Header().Get("ETag") == "" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Body.String(), "altcha-widget") {
		t.Fatalf("widget headers %v", rec.Header())
	}
	rec = f.visitor(http.MethodGet, "/_dropsite/altcha.js", "", "", withHeader("If-None-Match", rec.Header().Get("ETag")))
	wantStatus(t, rec, http.StatusNotModified)

	min := forms.MinAltchaMaxNumber
	if _, err := f.env.Svc.UpdateFormSettings(context.Background(), f.env.Admin, service.FormSettingsUpdate{AltchaMaxNumber: &min}); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, f.do(http.MethodPatch, "/api/v1/sites/"+f.site.ID+"/forms/settings", f.owner, map[string]bool{"require_altcha": true}), http.StatusOK)
	rec = f.visitor(http.MethodPost, "/_form/lead", formCT, "name=x")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "form_captcha_invalid") {
		t.Fatalf("no solution %d %s", rec.Code, rec.Body.String())
	}
	rec = f.visitor(http.MethodGet, "/_form/challenge", "", "")
	wantStatus(t, rec, http.StatusOK)
	var ch altcha.Challenge
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil || ch.Signature == "" {
		t.Fatalf("challenge %s", rec.Body.String())
	}
	sol, _ := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: time.Minute})
	raw, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	body := "name=x&altcha=" + strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(base64.StdEncoding.EncodeToString(raw))
	wantStatus(t, f.visitor(http.MethodPost, "/_form/lead", formCT, body), http.StatusOK)
	wantStatus(t, f.visitor(http.MethodPost, "/_form/lead", formCT, body), http.StatusBadRequest)
}

func TestFormChannelEndpoints(t *testing.T) {
	f := newFormHarness(t)
	base := "/api/v1/sites/" + f.site.ID + "/forms/channels"
	rec := f.do(http.MethodPost, base, f.owner, map[string]string{"kind": "webhook", "target": "https://hooks.example.com/x"})
	wantStatus(t, rec, http.StatusCreated)
	created := decodeAs[channelResultDTO](t, rec)
	if !strings.HasPrefix(created.Secret, "whsec_") || created.Channel.Status != "active" {
		t.Fatalf("created %+v", created)
	}
	rec = f.do(http.MethodGet, base, f.owner, nil)
	if strings.Contains(rec.Body.String(), created.Secret) || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("secret listed: %s", rec.Body.String())
	}
	rec = f.do(http.MethodPost, base+"/"+created.Channel.ID+"/secret", f.owner, nil)
	rotated := decodeAs[channelResultDTO](t, rec)
	if rotated.Secret == "" || rotated.Secret == created.Secret {
		t.Fatal("secret not rotated")
	}
	rec = f.do(http.MethodPatch, base+"/"+created.Channel.ID, f.owner, map[string]bool{"enabled": false})
	if decodeAs[channelDTO](t, rec).Enabled || !strings.Contains(rec.Body.String(), `"paused_at":null`) ||
		!strings.Contains(rec.Body.String(), `"consecutive_failures":0`) {
		t.Fatalf("not disabled: %s", rec.Body.String())
	}
	wantError(t, f.do(http.MethodPost, base, f.owner, map[string]string{"kind": "telegram"}), http.StatusBadRequest, "channel_unavailable")
	wantError(t, f.do(http.MethodPost, base, f.owner, map[string]string{"kind": "webhook", "target": "https://127.0.0.1/"}), http.StatusBadRequest, "channel_invalid")
	wantError(t, f.do(http.MethodPost, base+"/abc/verify", f.owner, map[string]string{"code": "1"}), http.StatusNotFound, "not_found")
	wantStatus(t, f.do(http.MethodDelete, base+"/"+created.Channel.ID, f.owner, nil), http.StatusNoContent)

	f.visitor(http.MethodPost, "/_form/lead", formCT, "name=%3D1%2B1")
	rec = f.do(http.MethodGet, "/api/v1/sites/"+f.site.ID+"/forms/submissions.csv", f.owner, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.Contains(rec.Body.String(), "'=1+1") || !strings.Contains(rec.Header().Get("Content-Disposition"), f.site.ID) ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("csv %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	// Without access the error is a JSON 404, not a CSV.
	wantError(t, f.do(http.MethodGet, "/api/v1/sites/nosuchsite/forms/submissions.csv", f.owner, nil), http.StatusNotFound, "not_found")
}

func TestAdminFormSettingsEndpoint(t *testing.T) {
	f := newFormHarness(t)
	wantError(t, f.do(http.MethodGet, "/api/v1/admin/forms/settings", f.owner, nil), http.StatusNotFound, "not_found")
	rec := f.do(http.MethodPatch, "/api/v1/admin/forms/settings", f.adminToken, map[string]any{
		"smtp":           map[string]any{"host": "smtp.example.com", "port": 587, "from": "forms@example.com", "password": "top-secret"},
		"retention_days": 30,
	})
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[formSettingsDTO](t, rec)
	if !got.SMTP.PasswordSet || got.RetentionDays != 30 || strings.Contains(rec.Body.String(), "top-secret") {
		t.Fatalf("settings %s", rec.Body.String())
	}
	if strings.Contains(f.logs.String(), "top-secret") {
		t.Fatal("secret in logs")
	}
	wantError(t, f.do(http.MethodPatch, "/api/v1/admin/forms/settings", f.adminToken, map[string]any{"retention_days": 0}),
		http.StatusBadRequest, "setting_invalid")
}
