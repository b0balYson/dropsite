package main

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestStdioGetSiteAttachedMatchesRemote(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Coffee", "coffee")
	if err != nil {
		t.Fatal(err)
	}

	out := same(t, local, web, "get_site", map[string]any{"site": "coffee"})
	at := out["attached"].(map[string]any)
	if len(at["forms"].([]any)) != 0 || at["channels"] != float64(0) ||
		!strings.Contains(at["summary"].(string), "No delivery channels") {
		t.Fatalf("empty attached: %v", at)
	}

	if _, err := r.env.Svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://hooks.example.com/a"); err != nil {
		t.Fatal(err)
	}
	p := service.FormPost{Host: v.Site.ID + "." + testenv.BaseDomain, Form: "lead", IP: "203.0.113.5"}
	p.Data.Fields = []domain.FormField{{Name: "name", Value: "Ann"}}
	if _, err := r.env.Svc.SubmitForm(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.env.Svc.SetSiteCounters(ctx, ann, v.Site.ID, domain.Counters{MetrikaID: "12345678"}, domain.SourceUI); err != nil {
		t.Fatal(err)
	}

	out = same(t, local, web, "get_site", map[string]any{"site": "coffee"})
	at = out["attached"].(map[string]any)
	if at["forms"].([]any)[0] != "lead" || at["channels"] != float64(1) || at["working_channels"] != float64(1) ||
		at["counters"].(map[string]any)["yandex_metrica_id"] != "12345678" ||
		!strings.Contains(at["summary"].(string), "all working") {
		t.Fatalf("attached: %v", at)
	}
	if lst := same(t, local, web, "list_sites", map[string]any{}); lst["sites"].([]any)[0].(map[string]any)["attached"] != nil {
		t.Fatal("list_sites must not carry attached")
	}
	same(t, local, web, "get_site", map[string]any{"site": "nope"})
}
