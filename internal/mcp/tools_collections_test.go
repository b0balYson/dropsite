package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// collectionsFake implements CollectionsBackend on top of the site fake.
type collectionsFake struct {
	*fakeBackend
	colls    map[string]Collection
	created  []CollectionInput
	patched  []CollectionPatch
	deleted  []string
	saved    []DocumentInput
	replaced bool
	query    DocumentQuery
	approved *bool
}

func newCollectionsFake() *collectionsFake {
	return &collectionsFake{fakeBackend: newFake(), colls: map[string]Collection{
		"menu": {Name: "menu", Mode: "public", Documents: 2, PublicURL: "https://promo.example.com/_dropsite/data/menu"},
	}}
}

func (f *collectionsFake) ListCollections(_ context.Context, ref string) ([]Collection, error) {
	if _, err := f.find(ref); err != nil {
		return nil, errors.New(NotFoundCollection)
	}
	out := []Collection{}
	for _, c := range f.colls {
		out = append(out, c)
	}
	return out, nil
}

func (f *collectionsFake) CreateCollection(_ context.Context, _ string, in CollectionInput) (Collection, error) {
	f.created = append(f.created, in)
	c := Collection{Name: strings.ToLower(in.Name), Mode: in.Mode}
	f.colls[c.Name] = c
	return c, nil
}

func (f *collectionsFake) UpdateCollection(_ context.Context, _, name string, in CollectionPatch) (Collection, error) {
	f.patched = append(f.patched, in)
	return f.colls[name], nil
}

func (f *collectionsFake) DeleteCollection(_ context.Context, _, name, confirm string) error {
	if name != confirm {
		return errors.New("confirm mismatch")
	}
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *collectionsFake) ListDocuments(_ context.Context, _, _ string, q DocumentQuery) (DocumentPage, error) {
	f.query = q
	return DocumentPage{NextCursor: "next"}, nil
}

func (f *collectionsFake) SaveDocuments(_ context.Context, _, name string, docs []DocumentInput, replace bool) ([]string, Collection, error) {
	f.saved, f.replaced = docs, replace
	return []string{"a"}, f.colls[name], nil
}

func (f *collectionsFake) DeleteDocuments(_ context.Context, _, name string, ids []string) (int, Collection, error) {
	return len(ids), f.colls[name], nil
}

func (f *collectionsFake) ModerateDocuments(_ context.Context, _, name string, ids []string, approve bool) (int, Collection, error) {
	f.approved = &approve
	return len(ids), f.colls[name], nil
}

var collectionTools = []string{"delete_collection", "delete_documents", "list_collections", "list_documents",
	"moderate_documents", "save_collection", "save_documents"}

func TestCollectionToolsRegisteredOnlyWithOption(t *testing.T) {
	f := newCollectionsFake()
	res := func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil }
	tools := listFormTools(t, connect(t, NewServer("test", res, Options{Files: true})))
	for _, n := range collectionTools {
		if tools[n] != nil {
			t.Fatalf("%s registered without Collections", n)
		}
	}
	tools = listFormTools(t, connect(t, NewServer("test", res, Options{Collections: true})))
	for _, n := range collectionTools {
		if tools[n] == nil {
			t.Fatalf("%s missing", n)
		}
	}
	for _, n := range []string{"list_collections", "list_documents"} {
		if !tools[n].Annotations.ReadOnlyHint {
			t.Errorf("%s not read-only", n)
		}
	}
	for _, n := range []string{"delete_collection", "save_documents", "delete_documents"} {
		if d := tools[n].Annotations.DestructiveHint; d == nil || !*d {
			t.Errorf("%s not destructive", n)
		}
	}
	if !strings.Contains(tools["save_documents"].Description, "confirm") ||
		!strings.Contains(tools["list_collections"].Description, "rather than in files") {
		t.Error("descriptions must explain confirmation and when to use collections")
	}
}

func TestCollectionToolsArguments(t *testing.T) {
	f := newCollectionsFake()
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Collections: true}))

	out, isErr, _ := call(t, cs, "list_collections", map[string]any{"site": "promo"})
	colls := out["collections"].([]any)
	if isErr || len(colls) != 1 || !strings.Contains(colls[0].(map[string]any)["snippet"].(string), "replaceChildren") {
		t.Fatalf("list: %v", out)
	}
	if _, isErr, text := call(t, cs, "list_collections", map[string]any{"site": "nope"}); !isErr || !strings.Contains(text, "list_collections") {
		t.Fatalf("missing site: %q", text)
	}

	out, _, _ = call(t, cs, "save_collection", map[string]any{"site": "promo", "name": "Reviews", "mode": "Moderated",
		"schema": map[string]any{"type": "object"}})
	if out["created"] != true || len(f.created) != 1 || f.created[0].Mode != "moderated" || f.created[0].Schema == nil {
		t.Fatalf("create: %v %+v", out, f.created)
	}
	out, _, _ = call(t, cs, "save_collection", map[string]any{"site": "promo", "name": "menu", "remove_schema": true})
	if out["created"] != false || len(f.patched) != 1 || !f.patched[0].RemoveSchema || f.patched[0].Title != nil || f.patched[0].Mode != nil {
		t.Fatalf("update: %v %+v", out, f.patched)
	}

	if _, isErr, text := call(t, cs, "delete_collection", map[string]any{"site": "promo", "name": "menu", "confirm": "nope"}); !isErr ||
		!strings.Contains(text, "not deleted") || len(f.deleted) != 0 {
		t.Fatalf("unconfirmed delete: %q", text)
	}
	out, isErr, _ = call(t, cs, "delete_collection", map[string]any{"site": "promo", "name": "MENU", "confirm": "menu"})
	if isErr || out["deleted"] != "menu" || !strings.Contains(out["summary"].(string), "2 document") {
		t.Fatalf("delete: %v", out)
	}
	f.colls["menu"] = Collection{Name: "menu", Mode: "public"}

	if _, isErr, text := call(t, cs, "list_documents", map[string]any{"site": "promo", "collection": "menu", "limit": 501}); !isErr ||
		!strings.Contains(text, "limit") {
		t.Fatalf("limit: %q", text)
	}
	out, _, _ = call(t, cs, "list_documents", map[string]any{"site": "promo", "collection": "menu", "status": " Pending ", "limit": 5, "cursor": "c"})
	if f.query != (DocumentQuery{Status: "pending", Cursor: "c", Limit: 5}) || out["next_cursor"] != "next" || out["documents"] == nil {
		t.Fatalf("documents: %v %+v", out, f.query)
	}

	out, _, _ = call(t, cs, "save_documents", map[string]any{"site": "promo", "collection": "Menu", "replace": true,
		"documents": []map[string]any{{"id": "a", "data": map[string]any{"price": 1}, "position": 3}}})
	if !f.replaced || len(f.saved) != 1 || *f.saved[0].Position != 3 || !strings.Contains(out["summary"].(string), "replaced") {
		t.Fatalf("save: %v %+v", out, f.saved)
	}
	if _, isErr, _ := call(t, cs, "moderate_documents", map[string]any{"site": "promo", "collection": "menu", "ids": []string{"a"},
		"action": "publish"}); !isErr {
		t.Fatal("bad action accepted")
	}
	out, _, _ = call(t, cs, "moderate_documents", map[string]any{"site": "promo", "collection": "menu", "ids": []string{"a"}, "action": "reject"})
	if f.approved == nil || *f.approved || !strings.Contains(out["summary"].(string), "rejected") {
		t.Fatalf("moderate: %v", out)
	}
}
