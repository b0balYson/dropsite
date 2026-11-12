package ui

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

var loginCmdRe = regexp.MustCompile(`dropsite login --server https://api\.pp\.test --token (ds_[A-Za-z0-9]+)`)

var codeCmdRe = regexp.MustCompile(`install\.sh \| DROPSITE_SERVER=https://api\.pp\.test DROPSITE_CODE=([0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{5}) sh`)

func TestConnectPage(t *testing.T) {
	h := newHarness(t)
	h.loginUser("alice@pp.test")
	p := h.get("/connect")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, `action="/connect/code"`, "https://api.pp.test/mcp", "Ключей пока нет",
		"Чат-клиенты пока не подключены", "ChatGPT", "claude.ai")
	// The manual skill instructions are gone.
	wantNotContains(t, p.body, "SKILL.md", "DROPSITE_CODE=")
	// The chat comes first, then the code and the computer command, then
	// example requests in the interface language.
	chat, code, phrases := strings.Index(p.body, `id="chat-h"`), strings.Index(p.body, `action="/connect/code"`),
		strings.Index(p.body, `id="use-h"`)
	if chat < 0 || chat > code || code > phrases {
		t.Fatalf("block order: chat %d, code %d, phrases %d", chat, code, phrases)
	}
	wantContains(t, p.body, "claude mcp add --transport http dropsite https://api.pp.test/mcp",
		"Сделай сайт кофейни и опубликуй", "Покажи последние заявки", "Верни предыдущую версию",
		"Что вы делаете сами", "записи DNS")
	// The root domain is an administrator's request.
	wantNotContains(t, p.body, "корневой домен")
	en := h.get("/connect", "Accept-Language", "en")
	wantContains(t, en.body, "Make a coffee shop site and publish it", "Show visits for the week")

	admin := newHarness(t)
	admin.loginAdmin()
	wantContains(t, admin.get("/connect").body, "Поставь эту заглушку на корневой домен")
}

func TestConnectCodeCommands(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	p := h.submit("/connect", "/connect/code", nil)
	wantStatus(t, p, http.StatusOK)
	m := codeCmdRe.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("command not shown:\n%.3000s", p.body)
	}
	wantContains(t, p.body, "$env:DROPSITE_SERVER=&#39;https://api.pp.test&#39;; $env:DROPSITE_CODE=&#39;"+m[1]+"&#39;; irm ",
		`data-countdown="`, "Действует до", "Выпустить новый код")
	res, err := h.env.Svc.ExchangeConnectCode(context.Background(), m[1], "", "127.0.0.1")
	if err != nil || res.User.ID != alice.User.ID {
		t.Fatalf("code from the page does not work: %v", err)
	}
	hx := h.submit("/connect", "/connect/code", nil, "HX-Request", "true")
	wantStatus(t, hx, http.StatusOK)
	wantContains(t, hx.body, `id="connect-block"`)
	wantNotContains(t, hx.body, "<html")
	if codeCmdRe.FindStringSubmatch(hx.body) == nil {
		t.Fatal("htmx response lacks the command")
	}
}

func TestConnectedAppsRevoke(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	ctx := context.Background()
	c, err := h.env.Svc.CreateConnectCode(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	code, err := h.env.Svc.AuthorizeOAuth(ctx, service.AuthorizeRequest{
		ClientID: "c1", ClientName: "ChatGPT <b>", RedirectURI: "https://chatgpt.com/connector_platform_oauth_redirect",
		Challenge: service.PKCEChallenge(verifier),
	}, c.Code, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.env.Svc.ExchangeOAuthCode(ctx, code, verifier, "c1", "https://chatgpt.com/connector_platform_oauth_redirect", ""); err != nil {
		t.Fatal(err)
	}
	p := h.get("/connect")
	wantContains(t, p.body, "ChatGPT &lt;b&gt;", "chatgpt.<wbr>com", "Отключить")
	grants, _ := h.env.Svc.ListOAuthGrants(ctx, alice)
	rev := h.submit("/connect", "/connect/apps/"+strconv.FormatInt(grants[0].ID, 10)+"/revoke", nil)
	wantStatus(t, rev, http.StatusSeeOther)
	wantContains(t, h.follow(rev).body, "Приложение отключено", "Чат-клиенты пока не подключены")
	wantStatus(t, h.submit("/connect", "/connect/apps/x/revoke", nil), http.StatusNotFound)
	wantStatus(t, h.follow(h.submit("/connect", "/connect/apps/"+strconv.FormatInt(grants[0].ID, 10)+"/revoke", nil)), http.StatusNotFound)
}

func TestTokenIsShownOnce(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")

	p := h.submit("/connect", "/connect/tokens", url.Values{"name": {"Laptop"}, "ttl": {"30"}})
	wantStatus(t, p, http.StatusOK)
	m := loginCmdRe.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("login command not shown:\n%.2000s", p.body)
	}
	wantContains(t, p.body, "показывается только один раз", "Действует до")
	if _, err := h.env.Svc.AuthenticateToken(context.Background(), m[1], "127.0.0.1"); err != nil {
		t.Fatalf("issued token does not work: %v", err)
	}

	again := h.get("/connect")
	wantNotContains(t, again.body, m[1])
	wantContains(t, again.body, "Laptop", "действует")

	// htmx variant swaps the form block and refreshes the list out of band.
	hx := h.submit("/connect", "/connect/tokens", url.Values{"name": {"CI"}, "ttl": {"0"}}, "HX-Request", "true")
	wantStatus(t, hx, http.StatusOK)
	wantContains(t, hx.body, `id="token-block"`, `hx-swap-oob="true"`, "CI")
	wantNotContains(t, hx.body, "<html")
	if loginCmdRe.FindStringSubmatch(hx.body) == nil {
		t.Fatal("htmx response lacks the command")
	}

	bad := h.submit("/connect", "/connect/tokens", url.Values{"name": {" "}}, "HX-Request", "true")
	wantStatus(t, bad, http.StatusOK)
	wantContains(t, bad.body, "Название ключа")

	tokens, _ := h.env.Svc.ListTokens(context.Background(), alice)
	if len(tokens) != 2 {
		t.Fatalf("tokens = %d", len(tokens))
	}
	var laptop int64
	for _, tk := range tokens {
		if tk.Name == "Laptop" {
			laptop = tk.ID
		}
	}
	rev := h.submit("/connect", "/connect/tokens/"+strconv.FormatInt(laptop, 10)+"/revoke", nil)
	wantStatus(t, rev, http.StatusSeeOther)
	wantContains(t, h.follow(rev).body, "Ключ отозван", "отозван")
	if _, err := h.env.Svc.AuthenticateToken(context.Background(), m[1], "127.0.0.2"); err == nil {
		t.Fatal("revoked token still works")
	}
	wantStatus(t, h.submit("/connect", "/connect/tokens/abc/revoke", nil), http.StatusNotFound)
	wantStatus(t, h.follow(h.submit("/connect", "/connect/tokens/99999/revoke", nil)), http.StatusNotFound)
}
