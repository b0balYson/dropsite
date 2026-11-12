package mcp

import (
	"strings"
	"testing"
)

func TestNewSiteAttachedSummary(t *testing.T) {
	empty := NewSiteAttached(nil, 0, 0, false, CounterIDs{}, CounterIDs{})
	if empty.Forms == nil || empty.AdminCounters ||
		empty.Summary != "No form requests stored. No delivery channels: requests are only stored. No own analytics counters." {
		t.Fatalf("empty: %+v", empty)
	}
	full := NewSiteAttached([]string{"lead", "order"}, 3, 2, true, CounterIDs{YandexMetricaID: "123", GoogleAnalyticsID: "G-1"},
		CounterIDs{GoogleAnalyticsID: "G-ADMIN"})
	for _, want := range []string{"Forms with requests: lead, order.", "Delivery channels: 3, working: 2.",
		"anti-robot check", "Yandex Metrica 123, Google Analytics G-1", "administrator adds counters"} {
		if !strings.Contains(full.Summary, want) {
			t.Errorf("summary %q lacks %q", full.Summary, want)
		}
	}
	if !full.AdminCounters {
		t.Error("admin counters not flagged")
	}
}

func TestSiteAttachedWithData(t *testing.T) {
	a := NewSiteAttached(nil, 0, 0, false, CounterIDs{}, CounterIDs{})
	if a.Collections == nil || a.ProxyEndpoints == nil || strings.Contains(a.Summary, "collections") {
		t.Fatalf("no data: %+v", a)
	}
	a = a.WithData([]AttachedCollection{{Name: "menu", Mode: "public", Documents: 12},
		{Name: "reviews", Mode: "moderated", Documents: 4, Pending: 2}}, []string{"openai"})
	for _, want := range []string{"Data collections: menu (public, 12 documents), reviews (moderated, 4 documents, 2 waiting for approval).",
		"API key proxies: openai."} {
		if !strings.Contains(a.Summary, want) {
			t.Errorf("summary %q lacks %q", a.Summary, want)
		}
	}
	if b := a.WithData(nil, nil); b.Collections == nil || b.ProxyEndpoints == nil {
		t.Fatalf("nil data: %+v", b)
	}
}
