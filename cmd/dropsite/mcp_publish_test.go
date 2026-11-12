package main

import (
	"context"
	"strings"
	"testing"
)

func TestStdioPublishSettingsTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	if _, err := r.env.Svc.CreateSite(ctx, ann, "Coffee", "coffee"); err != nil {
		t.Fatal(err)
	}

	got := same(t, local, web, "get_publish_settings", map[string]any{"site": "coffee"})
	if got["optimize"] != true || got["effective"] != true || got["installation_enabled"] != true ||
		!strings.Contains(got["summary"].(string), "WebP") {
		t.Fatalf("defaults: %v", got)
	}
	out := mustTool(t, local, "set_publish_settings", map[string]any{"site": "coffee", "optimize": false})
	if out["optimize"] != false || out["effective"] != false {
		t.Fatalf("set: %v", out)
	}
	same(t, local, web, "set_publish_settings", map[string]any{"site": "coffee", "optimize": false})
	same(t, local, web, "get_publish_settings", map[string]any{"site": "nope"})

	bob := r.env.User("bob@pp.test")
	if _, err := r.env.Svc.CreateSite(ctx, bob, "Bob", "bobs"); err != nil {
		t.Fatal(err)
	}
	same(t, local, web, "set_publish_settings", map[string]any{"site": "bobs", "optimize": true})
}
