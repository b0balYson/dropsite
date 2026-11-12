package main

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

func TestStdioCollectionsTools(t *testing.T) {
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

	l := same(t, local, web, "list_collections", map[string]any{"site": "cafe"})
	if !strings.Contains(l["summary"].(string), "no data collections") {
		t.Fatalf("empty list: %v", l)
	}
	out := mustTool(t, local, "save_collection", map[string]any{"site": "cafe", "name": "Menu", "title": "Menu"})
	c := out["collection"].(map[string]any)
	if out["created"] != true || c["name"] != "menu" || c["mode"] != "public" ||
		!strings.HasSuffix(c["public_url"].(string), "/_dropsite/data/menu") || !strings.Contains(c["snippet"].(string), "fetch('/_dropsite/data/menu')") {
		t.Fatalf("create: %v", out)
	}
	same(t, local, web, "save_collection", map[string]any{"site": "cafe", "name": "reviews", "mode": "moderated"})

	w := mustTool(t, local, "save_documents", map[string]any{"site": "cafe", "collection": "menu", "documents": []map[string]any{
		{"id": "cappuccino", "data": map[string]any{"name": "Cappuccino", "price": 200}, "position": 1},
		{"id": "tea", "data": map[string]any{"name": "Tea", "price": 100}},
	}})
	if w["count"] != float64(2) || !strings.Contains(w["summary"].(string), "no new release") {
		t.Fatalf("save: %v", w)
	}
	docs := same(t, local, web, "list_documents", map[string]any{"site": "cafe", "collection": "menu"})
	if len(docs["documents"].([]any)) != 2 {
		t.Fatalf("documents: %v", docs)
	}
	same(t, local, web, "list_documents", map[string]any{"site": "cafe", "collection": "menu", "limit": 1})
	same(t, local, web, "list_documents", map[string]any{"site": "cafe", "collection": "menu", "status": "nope"})
	same(t, local, web, "list_documents", map[string]any{"site": "cafe", "collection": "nope"})
	same(t, local, web, "list_documents", map[string]any{"site": "cafe", "collection": "menu", "cursor": "zzz"})

	// Changing a price from the chat: no release, the site reads the new value.
	mustTool(t, web, "save_documents", map[string]any{"site": "cafe", "collection": "menu",
		"documents": []map[string]any{{"id": "cappuccino", "data": map[string]any{"name": "Cappuccino", "price": 250}}}})
	if d, _, err := r.env.Svc.ReadDocument(ctx, host, "menu", "cappuccino"); err != nil || !strings.Contains(string(d.Data), "250") || d.Position != 1 {
		t.Fatalf("price: %+v %v", d, err)
	}

	// Schema errors are the same on both servers.
	mustTool(t, local, "save_collection", map[string]any{"site": "cafe", "name": "menu",
		"schema": map[string]any{"type": "object", "properties": map[string]any{"price": map[string]any{"type": "number"}}}})
	same(t, local, web, "save_documents", map[string]any{"site": "cafe", "collection": "menu",
		"documents": []map[string]any{{"id": "x", "data": map[string]any{"price": "free"}}}})
	wantToolErr(t, local, "save_documents", map[string]any{"site": "cafe", "collection": "menu",
		"documents": []map[string]any{{"id": "x", "data": map[string]any{"price": "free"}}}}, "document_invalid")

	// Moderation from the chat.
	mustTool(t, local, "save_collection", map[string]any{"site": "cafe", "name": "reviews", "mode": "moderated",
		"schema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}})
	if _, err := r.env.Svc.SubmitDocument(ctx, service.DataPost{Host: host, Collection: "reviews", IP: "198.51.100.3",
		Body: []byte(`{"text":"Tasty"}`)}); err != nil {
		t.Fatal(err)
	}
	pending := same(t, local, web, "list_documents", map[string]any{"site": "cafe", "collection": "reviews", "status": "pending"})
	id := pending["documents"].([]any)[0].(map[string]any)["id"].(string)
	wantToolErr(t, local, "moderate_documents", map[string]any{"site": "cafe", "collection": "reviews", "ids": []string{id}, "action": "maybe"}, "approve or reject")
	m := mustTool(t, local, "moderate_documents", map[string]any{"site": "cafe", "collection": "reviews", "ids": []string{id}, "action": "approve"})
	if m["count"] != float64(1) || m["pending"] != float64(0) {
		t.Fatalf("moderate: %v", m)
	}
	if p, err := r.env.Svc.ReadCollection(ctx, service.DataRead{Host: host, Collection: "reviews"}); err != nil || len(p.Items) != 1 {
		t.Fatalf("approved review: %+v %v", p, err)
	}

	same(t, local, web, "delete_documents", map[string]any{"site": "cafe", "collection": "menu", "ids": []string{"nope"}})
	d := mustTool(t, local, "delete_documents", map[string]any{"site": "cafe", "collection": "menu", "ids": []string{"tea"}})
	if d["count"] != float64(1) {
		t.Fatalf("delete documents: %v", d)
	}
	same(t, local, web, "list_collections", map[string]any{"site": "cafe"})
	wantToolErr(t, local, "delete_collection", map[string]any{"site": "cafe", "name": "menu", "confirm": "men"}, "not deleted")
	same(t, local, web, "delete_collection", map[string]any{"site": "cafe", "name": "menu", "confirm": "x"})
	mustTool(t, local, "delete_collection", map[string]any{"site": "cafe", "name": "menu", "confirm": "menu"})

	bob := r.env.Token(r.env.User("bob@pp.test"), "cli")
	bl, bw := r.stdio(bob), r.web(bob)
	for name, args := range map[string]map[string]any{
		"list_collections":   {"site": v.Site.ID},
		"save_collection":    {"site": v.Site.ID, "name": "x"},
		"delete_collection":  {"site": v.Site.ID, "name": "reviews", "confirm": "reviews"},
		"list_documents":     {"site": v.Site.ID, "collection": "reviews"},
		"save_documents":     {"site": v.Site.ID, "collection": "reviews", "documents": []map[string]any{{"data": map[string]any{}}}},
		"delete_documents":   {"site": v.Site.ID, "collection": "reviews", "ids": []string{id}},
		"moderate_documents": {"site": v.Site.ID, "collection": "reviews", "ids": []string{id}, "action": "reject"},
	} {
		same(t, bl, bw, name, args)
		wantToolErr(t, bl, name, args, "not found")
	}
}
