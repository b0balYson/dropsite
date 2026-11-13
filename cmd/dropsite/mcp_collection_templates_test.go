package main

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

func TestStdioCollectionTemplates(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe")
	if err != nil {
		t.Fatal(err)
	}
	host := v.Site.ID + "." + r.env.Config.BaseDomain

	brief := same(t, local, web, "list_collection_templates", map[string]any{})
	if len(brief["templates"].([]any)) != 7 {
		t.Fatalf("templates: %v", brief)
	}
	full := same(t, local, web, "list_collection_templates", map[string]any{"name": "reviews", "collection": "feedback"})
	tpl := full["templates"].([]any)[0].(map[string]any)
	if tpl["mode"] != "moderated" || !strings.Contains(tpl["snippet"].(string), "const NAME = 'feedback'") {
		t.Fatalf("full: %v", tpl)
	}
	same(t, local, web, "list_collection_templates", map[string]any{"name": "blog"})

	out := mustTool(t, local, "save_collection", map[string]any{"site": "cafe", "name": "menu", "template": "menu", "with_samples": true})
	c := out["collection"].(map[string]any)
	if c["documents"] != float64(4) || !strings.Contains(c["snippet"].(string), "data-category") {
		t.Fatalf("local create: %v", out)
	}
	out = mustTool(t, web, "save_collection", map[string]any{"site": "cafe", "name": "faq", "template": "faq"})
	if out["collection"].(map[string]any)["documents"] != float64(0) {
		t.Fatalf("remote create: %v", out)
	}
	if p, err := r.env.Svc.ReadCollection(ctx, service.DataRead{Host: host, Collection: "menu"}); err != nil || len(p.Items) != 4 {
		t.Fatalf("public menu: %d %v", len(p.Items), err)
	}
	// list_collections recognizes the template by its schema on both servers.
	l := same(t, local, web, "list_collections", map[string]any{"site": "cafe"})
	for _, x := range l["collections"].([]any) {
		if s := x.(map[string]any)["snippet"].(string); !strings.Contains(s, "<!-- Fallback content") {
			t.Fatalf("generic snippet for %v", x)
		}
	}
	// Errors are the same on both servers.
	same(t, local, web, "save_collection", map[string]any{"site": "cafe", "name": "menu", "template": "menu"})
	same(t, local, web, "save_collection", map[string]any{"site": "cafe", "name": "blog", "template": "blog"})
}
