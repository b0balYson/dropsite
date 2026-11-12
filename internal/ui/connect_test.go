package ui

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/skills"
)

var loginCmdRe = regexp.MustCompile(`dropsite login --server https://api\.pp\.test --token (ds_[A-Za-z0-9]+)`)

func TestConnectPage(t *testing.T) {
	h := newHarness(t)
	h.loginUser("alice@pp.test")
	p := h.get("/connect")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, installSh, installPs1, "~/.claude/skills/dropsite-publish/SKILL.md",
		"Ключей пока нет", "dropsite-publish")
	// Skill texts are shown escaped, not interpreted.
	firstLine := strings.SplitN(skills.Codex, "\n", 2)[0]
	wantContains(t, p.body, firstLine)
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
