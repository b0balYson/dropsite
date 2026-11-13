package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/datatpl"
)

// ListCollectionTemplates serves the built-in templates like the servers do.
func (f *collectionsFake) ListCollectionTemplates(context.Context) ([]CollectionTemplate, error) {
	var out []CollectionTemplate
	for _, t := range datatpl.All() {
		var schema map[string]any
		_ = json.Unmarshal(t.Schema, &schema)
		out = append(out, CollectionTemplate{Name: t.Name, Title: t.Title, Description: t.Description, Mode: t.Mode,
			Fields: TemplateFields(schema), Schema: schema, Snippet: t.Snippet(t.Name),
			Samples: []TemplateSample{{ID: "x", Data: map[string]any{"name": "X"}}}})
	}
	return out, nil
}

func TestListCollectionTemplatesTool(t *testing.T) {
	f := newCollectionsFake()
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Collections: true}))
	tools := listFormTools(t, cs)
	tl := tools["list_collection_templates"]
	if tl == nil || !tl.Annotations.ReadOnlyHint || !strings.Contains(tl.Description, "not a design") {
		t.Fatalf("tool %+v", tl)
	}
	if !strings.Contains(tools["save_collection"].Description, "template") {
		t.Fatal("save_collection does not mention templates")
	}

	out, isErr, _ := call(t, cs, "list_collection_templates", map[string]any{})
	list := out["templates"].([]any)
	first := list[0].(map[string]any)
	if isErr || len(list) != 7 || first["name"] != "menu" || first["snippet"] != nil || first["schema"] != nil ||
		first["fields"].([]any)[0] != "name" {
		t.Fatalf("brief list: %v", out)
	}

	out, isErr, _ = call(t, cs, "list_collection_templates", map[string]any{"name": " Menu ", "collection": "Drinks"})
	list = out["templates"].([]any)
	full := list[0].(map[string]any)
	if isErr || len(list) != 1 || full["schema"] == nil || len(full["samples"].([]any)) != 1 ||
		!strings.Contains(full["snippet"].(string), "const NAME = 'drinks'") {
		t.Fatalf("full template: %v", out)
	}
	if _, isErr, text := call(t, cs, "list_collection_templates", map[string]any{"name": "blog"}); !isErr ||
		!strings.Contains(text, "menu, catalog") {
		t.Fatalf("unknown template: %q", text)
	}
}

func TestSaveCollectionWithTemplate(t *testing.T) {
	f := newCollectionsFake()
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Collections: true}))
	out, isErr, text := call(t, cs, "save_collection", map[string]any{"site": "promo", "name": "faq", "template": " FAQ ",
		"with_samples": true})
	if isErr || len(f.created) != 1 || f.created[0].Template != "faq" || !f.created[0].WithSamples {
		t.Fatalf("create: %v %q %+v", out, text, f.created)
	}
	if !strings.Contains(out["summary"].(string), "from the faq template") {
		t.Fatalf("summary: %v", out["summary"])
	}
	// The existing collection refuses a template.
	if _, isErr, text := call(t, cs, "save_collection", map[string]any{"site": "promo", "name": "menu", "template": "menu"}); !isErr ||
		!strings.Contains(text, "only when a collection is created") || len(f.patched) != 0 {
		t.Fatalf("template on existing: %q", text)
	}
}

func TestSnippetFollowsTemplateSchema(t *testing.T) {
	menu, _ := datatpl.Get("menu")
	if s := SnippetFor("lunch", menu.Schema); !strings.Contains(s, "const NAME = 'lunch'") || !strings.Contains(s, "data-category") {
		t.Fatalf("template snippet: %.200s", s)
	}
	if s := SnippetFor("lunch", json.RawMessage(`{"type":"object"}`)); s != CollectionSnippet("lunch") {
		t.Fatal("generic snippet expected")
	}
	var schema map[string]any
	_ = json.Unmarshal(menu.Schema, &schema)
	if s := snippetOf(Collection{Name: "m", Schema: schema}); !strings.Contains(s, "const NAME = 'm'") {
		t.Fatal("snippetOf ignores the template")
	}
	if got := TemplateFields(schema); got[0] != "name" || got[1] != "price" || len(got) != 7 {
		t.Fatalf("fields %v", got)
	}
}
