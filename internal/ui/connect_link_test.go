package ui

import (
	"context"
	"net/http"
	"regexp"
	"testing"
)

var connectLinkRe = regexp.MustCompile(`https://api\.pp\.test/mcp/c/([0-9A-Z]{13})`)

func TestConnectURLOnPage(t *testing.T) {
	h := newHarness(t)
	h.loginUser("alice@pp.test")
	wantContains(t, h.get("/connect").body, `action="/connect/url"`, "Ссылка для подключения")
	p := h.submit("/connect", "/connect/url", nil, "HX-Request", "true")
	wantStatus(t, p, http.StatusOK)
	wantNotContains(t, p.body, "<html")
	m := connectLinkRe.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("connect URL not shown:\n%.3000s", p.body)
	}
	wantContains(t, p.body, `data-copy="#connect-link"`, "бета", "Новая ссылка для подключения", `data-countdown="`)
	if err := h.env.Svc.CheckConnectLink(context.Background(), m[1], "127.0.0.1"); err != nil {
		t.Fatalf("link from the page does not work: %v", err)
	}
	// Without htmx the whole page comes back with the link.
	full := h.submit("/connect", "/connect/url", nil)
	wantStatus(t, full, http.StatusOK)
	if connectLinkRe.FindStringSubmatch(full.body) == nil {
		t.Fatal("full page lacks the link")
	}
	// The session approval route needs the public admin listener.
	wantStatus(t, h.get("/connect/authorize?request=x"), http.StatusNotFound)
}
