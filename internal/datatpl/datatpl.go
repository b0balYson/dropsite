// Package datatpl holds the built-in templates of site data collections:
// a JSON Schema, a default mode, sample documents and a dependency-free
// HTML and JavaScript snippet that renders the collection as semantic,
// unstyled markup. Templates are a starting point the assistant adapts to
// the site, not a design. The package has no dependencies on the server,
// so the CLI links it too.
package datatpl

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Marker stands for the collection name in a snippet.
const Marker = "__COLLECTION__"

// order is the order templates are listed in.
var order = []string{"menu", "catalog", "services", "events", "team", "faq", "reviews"}

//go:embed templates
var files embed.FS

// Sample is one sample document of a template.
type Sample struct {
	ID       string          `json:"id"`
	Position int64           `json:"position"`
	Data     json.RawMessage `json:"data"`
}

// Template is a built-in collection template.
type Template struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Mode        string          `json:"mode"`
	Schema      json.RawMessage `json:"schema"`
	Samples     []Sample        `json:"samples"`
	snippet     string
	canonical   string
}

// Snippet returns the template's HTML and JavaScript for a collection
// name. Collection names are [a-z0-9_-], safe in an id and a JS string.
func (t Template) Snippet(collection string) string {
	return strings.ReplaceAll(t.snippet, Marker, collection)
}

var templates = mustLoad()

func mustLoad() []Template {
	out := make([]Template, 0, len(order))
	for _, name := range order {
		t, err := load(name)
		if err != nil {
			panic(fmt.Sprintf("datatpl: template %s: %v", name, err))
		}
		out = append(out, t)
	}
	return out
}

func load(name string) (Template, error) {
	raw, err := files.ReadFile("templates/" + name + ".json")
	if err != nil {
		return Template{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var t Template
	if err := dec.Decode(&t); err != nil {
		return Template{}, err
	}
	if t.Name != name {
		return Template{}, fmt.Errorf("name %q does not match the file", t.Name)
	}
	html, err := files.ReadFile("templates/" + name + ".html")
	if err != nil {
		return Template{}, err
	}
	t.snippet = string(html)
	if t.canonical, err = canonical(t.Schema); err != nil {
		return Template{}, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, t.Schema); err != nil {
		return Template{}, err
	}
	t.Schema = compact.Bytes()
	return t, nil
}

// canonical re-encodes JSON with sorted object keys.
func canonical(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// All returns every template in display order.
func All() []Template { return append([]Template(nil), templates...) }

// Names returns the template names in display order.
func Names() []string { return append([]string(nil), order...) }

// Get returns the template with the name.
func Get(name string) (Template, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, t := range templates {
		if t.Name == n {
			return t, true
		}
	}
	return Template{}, false
}

// ForSchema returns the template whose schema equals schema (key order and
// spacing aside): a collection created from a template keeps its snippet
// as long as its schema is unchanged.
func ForSchema(schema json.RawMessage) (Template, bool) {
	if len(schema) == 0 {
		return Template{}, false
	}
	c, err := canonical(schema)
	if err != nil {
		return Template{}, false
	}
	for _, t := range templates {
		if t.canonical == c {
			return t, true
		}
	}
	return Template{}, false
}
