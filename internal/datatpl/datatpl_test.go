package datatpl

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestBuiltInSet(t *testing.T) {
	want := []string{"menu", "catalog", "services", "events", "team", "faq", "reviews"}
	all := All()
	if len(all) != len(want) {
		t.Fatalf("%d templates, want %d", len(all), len(want))
	}
	for i, tpl := range all {
		if tpl.Name != want[i] || tpl.Title == "" || len(tpl.Description) < 40 {
			t.Errorf("template %d: %+v", i, tpl.Name)
		}
		wantMode := "public"
		if tpl.Name == "reviews" {
			wantMode = "moderated"
		}
		if tpl.Mode != wantMode {
			t.Errorf("%s: mode %s", tpl.Name, tpl.Mode)
		}
		if len(tpl.Samples) < 2 {
			t.Errorf("%s: %d samples", tpl.Name, len(tpl.Samples))
		}
		var s struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(tpl.Schema, &s); err != nil || s.Type != "object" {
			t.Errorf("%s: schema %s", tpl.Name, tpl.Schema)
		}
	}
	if _, ok := Get(" Menu "); !ok {
		t.Error("Get is not case-insensitive")
	}
	if _, ok := Get("blog"); ok {
		t.Error("unknown template found")
	}
}

var (
	externalRef = regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//`)
	styleAttr   = regexp.MustCompile(`(?i)\sstyle\s*=`)
	classAttr   = regexp.MustCompile(`(?i)\sclass\s*=`)
)

// TestSnippetsAreSafeAndUnstyled keeps the snippet rules of the spec.
func TestSnippetsAreSafeAndUnstyled(t *testing.T) {
	for _, tpl := range All() {
		s := tpl.Snippet("my-list")
		checks := map[string]bool{
			"name substituted":           !strings.Contains(s, Marker) && strings.Contains(s, `const NAME = 'my-list'`),
			"loads its collection":       strings.Contains(s, "fetch('/_dropsite/data/' + NAME"),
			"follows next":               strings.Contains(s, "body.next"),
			"keeps fallback on error":    strings.Contains(s, ".catch(() => { /* keep the fallback content */ })"),
			"has fallback content":       strings.Contains(s, "<!-- Fallback content"),
			"no innerHTML":               !strings.Contains(s, ".innerHTML") && !strings.Contains(s, "insertAdjacentHTML") && !strings.Contains(s, "document.write"),
			"no eval":                    !strings.Contains(s, "eval(") && !strings.Contains(s, "new Function"),
			"no style element":           !strings.Contains(strings.ToLower(s), "<style"),
			"no style attribute":         !styleAttr.MatchString(s),
			"no classes":                 !classAttr.MatchString(s),
			"no external resources":      !externalRef.MatchString(s) && !strings.Contains(s, "@import"),
			"labelled section":           strings.Contains(s, `<section id="my-list" aria-labelledby="my-list-title">`),
			"textContent for data":       strings.Contains(s, "textContent"),
			"single self-contained IIFE": strings.Count(s, "<script>") == 1 && strings.Contains(s, "(() => {"),
		}
		for what, ok := range checks {
			if !ok {
				t.Errorf("%s: %s", tpl.Name, what)
			}
		}
	}
	r, _ := Get("reviews")
	s := r.Snippet("reviews")
	for _, want := range []string{`name="_hp"`, `<altcha-widget challenge="/_form/challenge">`, `src="/_dropsite/altcha.js"`,
		`method: 'POST'`, `'Content-Type': 'application/json'`, `aria-live="polite"`, `r.status === 202`, `<label for="reviews-name">`} {
		if !strings.Contains(s, want) {
			t.Errorf("reviews snippet lacks %q", want)
		}
	}
}

func TestForSchema(t *testing.T) {
	menu, _ := Get("menu")
	// Key order and spacing do not matter.
	var m map[string]any
	if err := json.Unmarshal(menu.Schema, &m); err != nil {
		t.Fatal(err)
	}
	re, _ := json.MarshalIndent(m, "", "  ")
	if got, ok := ForSchema(re); !ok || got.Name != "menu" {
		t.Fatalf("ForSchema: %v %v", got.Name, ok)
	}
	m["required"] = []string{"name"}
	changed, _ := json.Marshal(m)
	if _, ok := ForSchema(changed); ok {
		t.Fatal("changed schema matched")
	}
	for _, raw := range []string{"", "null", "{", `{"type":"object"}`} {
		if _, ok := ForSchema(json.RawMessage(raw)); ok {
			t.Fatalf("%q matched", raw)
		}
	}
}

func TestReviewsSchemaListsOnlyVisitorFields(t *testing.T) {
	r, _ := Get("reviews")
	var s struct {
		Properties map[string]struct {
			MaxLength *int `json:"maxLength"`
			Maximum   *int `json:"maximum"`
		} `json:"properties"`
		Additional *bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(r.Schema, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Properties) != 3 || s.Additional == nil || *s.Additional {
		t.Fatalf("reviews schema %s", r.Schema)
	}
	for k, p := range s.Properties {
		if p.MaxLength == nil && p.Maximum == nil {
			t.Errorf("reviews field %s is unbounded", k)
		}
	}
}
