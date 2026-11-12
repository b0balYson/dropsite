package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
)

type openAPIDoc struct {
	OpenAPI    string                                `json:"openapi"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		Schemas         map[string]json.RawMessage `json:"schemas"`
		Responses       map[string]json.RawMessage `json:"responses"`
		Parameters      map[string]json.RawMessage `json:"parameters"`
	} `json:"components"`
}

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}

func TestOpenAPIServedWithoutToken(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/api/v1/openapi.json", "", nil)
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", rec.Header().Get("Content-Type"))
	}
	var doc openAPIDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" || doc.Components.SecuritySchemes["bearer"] == nil {
		t.Fatalf("doc header %q", doc.OpenAPI)
	}
}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	var doc openAPIDoc
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for path, item := range doc.Paths {
		for method := range item {
			if httpMethods[method] {
				documented[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	registered := map[string]bool{}
	for _, rt := range (&api{}).routes() {
		registered[rt.Method+" "+rt.Path] = true
	}
	if missing := diff(registered, documented); len(missing) > 0 {
		t.Errorf("routes missing from openapi.json: %v", missing)
	}
	if extra := diff(documented, registered); len(extra) > 0 {
		t.Errorf("openapi.json documents unknown routes: %v", extra)
	}
}

func TestOpenAPIRefsResolve(t *testing.T) {
	var doc openAPIDoc
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	sections := map[string]map[string]json.RawMessage{
		"schemas": doc.Components.Schemas, "responses": doc.Components.Responses, "parameters": doc.Components.Parameters,
	}
	for _, ref := range collectRefs(t, openAPIDocument) {
		parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
		if len(parts) != 2 || sections[parts[0]][parts[1]] == nil {
			t.Errorf("unresolved $ref %s", ref)
		}
	}
}

func collectRefs(t *testing.T, raw []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var refs []string
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			for k, val := range x {
				if s, ok := val.(string); ok && k == "$ref" {
					refs = append(refs, s)
				}
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	walk(v)
	return refs
}

func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
