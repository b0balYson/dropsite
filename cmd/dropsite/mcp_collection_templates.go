package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

// Collection templates of the local server, from the API: the assistant
// sees the templates the server accepts, whatever the CLI version.
func (b localBackend) ListCollectionTemplates(ctx context.Context) ([]mcp.CollectionTemplate, error) {
	ts, err := b.c.ListCollectionTemplates(ctx)
	if err != nil {
		return nil, collectionsError(err)
	}
	out := make([]mcp.CollectionTemplate, 0, len(ts))
	for _, t := range ts {
		schema := jsonObject(t.Schema)
		samples := make([]mcp.TemplateSample, 0, len(t.Samples))
		for _, s := range t.Samples {
			samples = append(samples, mcp.TemplateSample{ID: s.ID, Position: s.Position, Data: jsonObject(s.Data)})
		}
		out = append(out, mcp.CollectionTemplate{Name: t.Name, Title: t.Title, Description: t.Description, Mode: t.Mode,
			Fields: mcp.TemplateFields(schema), Schema: schema, Samples: samples, Snippet: t.Snippet})
	}
	return out, nil
}
