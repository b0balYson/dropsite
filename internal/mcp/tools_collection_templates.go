package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/datatpl"
)

// CollectionTemplate is a built-in collection template as the server
// lists it.
type CollectionTemplate struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description" jsonschema:"what the template is for"`
	Mode        string           `json:"mode" jsonschema:"default mode: public or moderated"`
	Fields      []string         `json:"fields" jsonschema:"document fields, required ones first"`
	Schema      map[string]any   `json:"schema,omitempty" jsonschema:"JSON Schema of the documents (only when one template is asked for)"`
	Samples     []TemplateSample `json:"samples,omitempty" jsonschema:"sample documents to adapt to the user's content"`
	Snippet     string           `json:"snippet,omitempty" jsonschema:"HTML and JavaScript that render the collection as unstyled semantic markup with fallback content; adapt texts, fallback and markup to the site and style it with the site's CSS"`
}

// TemplateSample is one sample document of a template.
type TemplateSample struct {
	ID       string         `json:"id"`
	Position int64          `json:"position"`
	Data     map[string]any `json:"data"`
}

// CollectionTemplateList is the output of list_collection_templates.
type CollectionTemplateList struct {
	Templates []CollectionTemplate `json:"templates"`
	Summary   string               `json:"summary" jsonschema:"what to do next"`
}

type listCollectionTemplatesArgs struct {
	Name       string `json:"name,omitempty" jsonschema:"template name for the full template (schema, samples, snippet); empty lists all templates briefly"`
	Collection string `json:"collection,omitempty" jsonschema:"collection name to use in the snippet (default: the template name)"`
}

func (t tools) registerCollectionTemplates(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_collection_templates", Annotations: readOnly("List collection templates"),
		Description: "List the built-in data templates of collections: menu (categories and prices), catalog " +
			"(products), services (price list), events (schedule), team, faq and reviews (moderated). Call it before " +
			"creating a collection for such content; with name it returns the template's JSON Schema, sample " +
			"documents and an unstyled snippet that loads the collection and keeps fallback content. A template is " +
			"a starting point, not a design: adapt fields, texts and markup to the site and style it with the " +
			"site's own CSS. Then call save_collection with template. These are data templates; starter site " +
			"designs are list_templates."}, t.listCollectionTemplates)
}

func (t tools) listCollectionTemplates(ctx context.Context, req *sdk.CallToolRequest, in listCollectionTemplatesArgs) (*sdk.CallToolResult, CollectionTemplateList, error) {
	b, err := t.collections(ctx, req)
	if err != nil {
		return nil, CollectionTemplateList{}, err
	}
	all, err := b.ListCollectionTemplates(ctx)
	if err != nil {
		return nil, CollectionTemplateList{}, err
	}
	name := normName(in.Name)
	if name == "" {
		out := make([]CollectionTemplate, 0, len(all))
		for _, tpl := range all {
			out = append(out, CollectionTemplate{Name: tpl.Name, Title: tpl.Title, Description: tpl.Description,
				Mode: tpl.Mode, Fields: tpl.Fields})
		}
		return nil, CollectionTemplateList{Templates: out, Summary: fmt.Sprintf("%d templates. Call again with name for "+
			"the schema, samples and snippet of the one that fits.", len(out))}, nil
	}
	for _, tpl := range all {
		if tpl.Name != name {
			continue
		}
		collection := normName(in.Collection)
		if collection == "" {
			collection = tpl.Name
		}
		if local, ok := datatpl.Get(tpl.Name); ok {
			tpl.Snippet = local.Snippet(collection)
		} else if collection != tpl.Name {
			tpl.Snippet = ""
		}
		return nil, CollectionTemplateList{Templates: []CollectionTemplate{tpl}, Summary: fmt.Sprintf(
			"Template %s. Create the collection with save_collection (template %q, with_samples true to start from the "+
				"samples), replace the samples with the user's content, then put the adapted snippet into the page.",
			tpl.Name, tpl.Name)}, nil
	}
	names := make([]string, 0, len(all))
	for _, tpl := range all {
		names = append(names, tpl.Name)
	}
	return nil, CollectionTemplateList{}, fmt.Errorf("no template %q; templates: %s", name, strings.Join(names, ", "))
}

// SnippetFor is the snippet of a collection: the template's one when the
// schema is a template's schema, the generic one otherwise.
func SnippetFor(name string, schema json.RawMessage) string {
	if tpl, ok := datatpl.ForSchema(schema); ok {
		return tpl.Snippet(name)
	}
	return CollectionSnippet(name)
}

func snippetOf(c Collection) string {
	if c.Schema == nil {
		return CollectionSnippet(c.Name)
	}
	raw, err := json.Marshal(c.Schema)
	if err != nil {
		return CollectionSnippet(c.Name)
	}
	return SnippetFor(c.Name, raw)
}

func createdSummary(c Collection, template string) string {
	if template == "" {
		return fmt.Sprintf("Collection %s created (%s). The site reads it at %s; add documents with save_documents.",
			c.Name, c.Mode, c.PublicURL)
	}
	s := fmt.Sprintf("Collection %s created from the %s template (%s); the site reads it at %s.", c.Name, template,
		c.Mode, c.PublicURL)
	if c.Documents > 0 {
		s += fmt.Sprintf(" It holds %d sample document(s): replace them with the user's content (save_documents with "+
			"replace after the user confirmed).", c.Documents)
	}
	return s + " Adapt the snippet's texts, fallback content and markup to the site and style it with the site's CSS."
}

// TemplateFields lists the top-level properties of a schema, required ones
// first, the rest sorted.
func TemplateFields(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var out []string
	seen := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok && !seen[s] {
				out = append(out, s)
				seen[s] = true
			}
		}
	}
	var rest []string
	for k := range props {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
