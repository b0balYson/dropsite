package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGuardSettings(t *testing.T) {
	h := newHarness(t)
	h.loginAdmin()
	ctx := context.Background()
	if _, err := h.env.Svc.AddBanCLI(ctx, "203.0.113.9", time.Hour); err != nil {
		t.Fatal(err)
	}
	const page = "/admin/settings"
	p := h.get(page)
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, "Защита от злоупотреблений", "203.0.113.9", "Разбанить", `name="defender" value="1" checked`)

	p = h.submit(page, page+"/guard", url.Values{"strict": {"1"}})
	wantStatus(t, p, http.StatusSeeOther)
	wantContains(t, h.follow(p).body, "Настройки защиты сохранены")
	st, _ := h.env.Svc.DefenderState(ctx)
	if st.Enabled || !st.Strict {
		t.Fatalf("defender: %+v", st)
	}
	root := h.env.Svc.Config().SitesDir
	if _, err := os.Stat(filepath.Join(root, "flags", "defender-off")); err != nil {
		t.Fatalf("marker: %v", err)
	}

	p = h.submit(page, page+"/bans/clear", url.Values{"key": {"203.0.113.9"}})
	wantStatus(t, p, http.StatusSeeOther)
	wantContains(t, h.follow(p).body, "Бан 203.0.113.9 снят", "Активных банов нет")
}

func TestProxyClassesScreen(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Bot", "")
	base := "/sites/" + v.Site.ID + "/proxy"
	empty := h.get(base)
	wantContains(t, empty.body, `name="class" value="ai" checked`, "Шаблон запроса", "Дополнительные настройки", "session.js")

	tmpl, _ := json.Marshal(map[string]any{"path": "/chat", "body": map[string]any{"q": "{{q}}"},
		"fields": map[string]any{"q": map[string]any{"type": "string"}}, "response": "answer"})
	p := h.submit(base, base, url.Values{"name": {"chat"}, "class": {"ai"}, "upstream_url": {"https://api.openai.com/v1"},
		"auth": {"bearer"}, "template": {string(tmpl)}, "daily_budget": {"25"}, "enabled": {"1"}})
	wantStatus(t, p, http.StatusSeeOther)
	list := h.follow(p)
	wantContains(t, list.body, "ИИ-модель", "сегодня 0 из 25", "20 в минуту", "50 на сессию")

	bad := h.submit(base, base, url.Values{"name": {"x"}, "class": {"ai"}, "upstream_url": {"https://api.openai.com/v1"},
		"auth": {"bearer"}, "template": {"{not json"}, "enabled": {"1"}})
	wantStatus(t, bad, http.StatusUnprocessableEntity)

	p = h.submit(base, base, url.Values{"name": {"raw"}, "class": {"transparent"}, "upstream_url": {"https://api.openai.com/v1"},
		"auth": {"bearer"}, "methods": {"POST"}, "enabled": {"1"}})
	wantContains(t, h.follow(p).body, "Класс «прозрачный»")
	edit := h.get(base + "/chat/edit")
	wantContains(t, edit.body, `name="class" value="ai" checked`, `value="25"`, "&#34;response&#34;: &#34;answer&#34;")
}
