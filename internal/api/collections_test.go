package api

import (
	"net/http"
	"strings"
	"testing"
)

const reviewSchemaJSON = `{"type":"object","properties":{"name":{"type":"string"},"text":{"type":"string"}},"required":["name","text"]}`

func TestCollectionsAPI(t *testing.T) {
	f := newFormHarness(t)
	base := "/api/v1/sites/" + f.site.ID + "/collections"

	rec := f.do(http.MethodPost, base, f.owner, map[string]any{"name": "menu", "mode": "public"})
	wantStatus(t, rec, http.StatusCreated)
	c := decodeAs[collectionDTO](t, rec)
	if c.Name != "menu" || string(c.Schema) != "null" || !strings.HasSuffix(c.PublicURL, "/_dropsite/data/menu") {
		t.Fatalf("created %+v", c)
	}
	rec = f.do(http.MethodPost, base+"/menu/documents", f.owner, map[string]any{"documents": []map[string]any{
		{"id": "cappuccino", "data": map[string]any{"price": 200}, "position": 1},
		{"data": map[string]any{"price": 150}},
	}})
	wantStatus(t, rec, http.StatusOK)
	if w := decodeAs[saveDocumentsReply](t, rec); len(w.IDs) != 2 || w.Collection.Documents != 2 {
		t.Fatalf("written %+v", w)
	}
	rec = f.do(http.MethodGet, base+"/menu/documents", f.owner, nil)
	wantStatus(t, rec, http.StatusOK)
	p := decodeAs[documentPageDTO](t, rec)
	if len(p.Items) != 2 || p.Items[0].Status != "approved" || p.NextCursor != nil {
		t.Fatalf("documents %+v", p)
	}
	wantError(t, f.do(http.MethodGet, base+"/menu/documents?status=nope", f.owner, nil), http.StatusBadRequest, "invalid_input")
	wantError(t, f.do(http.MethodGet, base+"/menu/documents?limit=501", f.owner, nil), http.StatusBadRequest, "invalid_input")

	// Schema errors name the document and the field.
	rec = f.do(http.MethodPatch, base+"/menu", f.owner, map[string]any{"schema": map[string]any{"type": "object",
		"properties": map[string]any{"price": map[string]any{"type": "number"}}}})
	wantStatus(t, rec, http.StatusOK)
	e := wantError(t, f.do(http.MethodPost, base+"/menu/documents", f.owner, map[string]any{"documents": []map[string]any{
		{"id": "a", "data": map[string]any{"price": 1}}, {"id": "b", "data": map[string]any{"price": "x"}},
	}}), http.StatusBadRequest, "document_invalid")
	if e.Details["index"] != float64(1) || e.Details["field"] != "price" {
		t.Fatalf("details %v", e.Details)
	}
	rec = f.do(http.MethodPatch, base+"/menu", f.owner, `{"schema":null,"title":"Menu"}`)
	wantStatus(t, rec, http.StatusOK)
	if c := decodeAs[collectionDTO](t, rec); string(c.Schema) != "null" || c.Title != "Menu" {
		t.Fatalf("patched %+v", c)
	}

	// Moderated collections and moderation.
	wantError(t, f.do(http.MethodPost, base, f.owner, map[string]any{"name": "reviews", "mode": "moderated"}),
		http.StatusBadRequest, "collection_invalid")
	rec = f.do(http.MethodPost, base, f.owner, `{"name":"reviews","mode":"moderated","schema":`+reviewSchemaJSON+`}`)
	wantStatus(t, rec, http.StatusCreated)
	rec = f.visitor(http.MethodPost, "/_dropsite/data/reviews", "application/json", `{"name":"Anna","text":"Tasty"}`)
	wantStatus(t, rec, http.StatusAccepted)
	if !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatalf("proposal %s", rec.Body.String())
	}
	rec = f.do(http.MethodGet, base+"/reviews/documents?status=pending", f.owner, nil)
	pending := decodeAs[documentPageDTO](t, rec)
	if len(pending.Items) != 1 {
		t.Fatalf("pending %+v", pending)
	}
	wantError(t, f.do(http.MethodPost, base+"/reviews/moderate", f.owner, map[string]any{"ids": []string{"x"}, "action": "maybe"}),
		http.StatusBadRequest, "invalid_input")
	rec = f.do(http.MethodPost, base+"/reviews/moderate", f.owner, map[string]any{"ids": []string{pending.Items[0].ID}, "action": "approve"})
	wantStatus(t, rec, http.StatusOK)
	if n := decodeAs[countReply](t, rec); n.Count != 1 || n.Collection.Pending != 0 {
		t.Fatalf("moderated %+v", n)
	}
	rec = f.visitor(http.MethodGet, "/_dropsite/data/reviews", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Anna") {
		t.Fatalf("public %d %s", rec.Code, rec.Body.String())
	}

	// Deleting needs the name repeated.
	wantError(t, f.do(http.MethodDelete, base+"/menu", f.owner, map[string]any{"confirm": "men"}), http.StatusBadRequest, "invalid_input")
	wantStatus(t, f.do(http.MethodDelete, base+"/menu/documents/cappuccino", f.owner, nil), http.StatusNoContent)
	wantError(t, f.do(http.MethodDelete, base+"/menu/documents/cappuccino", f.owner, nil), http.StatusNotFound, "not_found")
	rec = f.do(http.MethodPost, base+"/menu/delete-documents", f.owner, map[string]any{"ids": []string{"nope"}})
	wantStatus(t, rec, http.StatusOK)
	wantStatus(t, f.do(http.MethodDelete, base+"/menu?confirm=menu", f.owner, nil), http.StatusNoContent)

	// Another user's collections do not exist.
	_, bob := f.user("bob@pp.test")
	wantError(t, f.do(http.MethodGet, base, bob, nil), http.StatusNotFound, "not_found")
	wantError(t, f.do(http.MethodPost, base+"/reviews/documents", bob, map[string]any{"documents": []any{}}), http.StatusNotFound, "not_found")
}

func TestSiteDataPaths(t *testing.T) {
	f := newFormHarness(t)
	base := "/api/v1/sites/" + f.site.ID + "/collections"
	wantStatus(t, f.do(http.MethodPost, base, f.owner, map[string]any{"name": "menu"}), http.StatusCreated)
	wantStatus(t, f.do(http.MethodPost, base, f.owner, `{"name":"reviews","mode":"moderated","schema":`+reviewSchemaJSON+`}`),
		http.StatusCreated)
	wantStatus(t, f.do(http.MethodPost, base+"/menu/documents", f.owner,
		map[string]any{"documents": []map[string]any{{"id": "tea", "data": map[string]any{"price": 100}}}}), http.StatusOK)

	rec := f.visitor(http.MethodGet, "/_dropsite/data/menu", "", "")
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Cache-Control") != "no-cache" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("headers %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), `"next":null`) || !strings.Contains(rec.Body.String(), `"data":{"price":100}`) {
		t.Fatalf("body %s", rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	wantStatus(t, f.visitor(http.MethodGet, "/_dropsite/data/menu", "", "", withHeader("If-None-Match", etag)), http.StatusNotModified)
	wantStatus(t, f.visitor(http.MethodGet, "/_dropsite/data/menu/tea", "", ""), http.StatusOK)
	wantStatus(t, f.do(http.MethodPost, base+"/menu/documents", f.owner,
		map[string]any{"documents": []map[string]any{{"id": "tea", "data": map[string]any{"price": 120}}}}), http.StatusOK)
	rec = f.visitor(http.MethodGet, "/_dropsite/data/menu", "", "", withHeader("If-None-Match", etag))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "120") {
		t.Fatalf("after change %d %s", rec.Code, rec.Body.String())
	}

	wantError(t, f.visitor(http.MethodGet, "/_dropsite/data/nope", "", ""), http.StatusNotFound, "not_found")
	wantError(t, f.visitor(http.MethodGet, "/_dropsite/data/menu/nope", "", ""), http.StatusNotFound, "not_found")
	wantError(t, f.visitor(http.MethodGet, "/_dropsite/data/menu/a/b", "", ""), http.StatusNotFound, "not_found")
	wantError(t, f.visitor(http.MethodPost, "/_dropsite/data/menu", "application/json", `{}`), http.StatusMethodNotAllowed, "collection_read_only")
	wantError(t, f.visitor(http.MethodPut, "/_dropsite/data/menu", "application/json", `{}`), http.StatusMethodNotAllowed, "method_not_allowed")
	wantError(t, f.visitor(http.MethodPost, "/_dropsite/data/reviews", "application/json", `{"name":"A","text":"B","is_admin":true}`),
		http.StatusBadRequest, "document_invalid")
	wantError(t, f.visitor(http.MethodPost, "/_dropsite/data/reviews", "text/plain", `{"name":"A","text":"B"}`),
		http.StatusUnsupportedMediaType, "form_unsupported_type")
	wantError(t, f.visitor(http.MethodPost, "/_dropsite/data/reviews", "application/json", `{"name":"A","text":"B"}`,
		withHeader("Origin", "https://evil.example")), http.StatusForbidden, "form_origin_mismatch")
	big := `{"name":"A","text":"` + strings.Repeat("x", 9<<10) + `"}`
	wantError(t, f.visitor(http.MethodPost, "/_dropsite/data/reviews", "application/json", big), http.StatusRequestEntityTooLarge, "document_invalid")
	rec = f.visitor(http.MethodPost, "/_dropsite/data/reviews", "application/json", `{"name":"A","text":"B","_hp":"bot"}`)
	wantStatus(t, rec, http.StatusAccepted)
	rec = f.visitor(http.MethodGet, "/_dropsite/data/reviews", "", "")
	if strings.Contains(rec.Body.String(), `"name"`) {
		t.Fatalf("pending or honeypot visible: %s", rec.Body.String())
	}
}
