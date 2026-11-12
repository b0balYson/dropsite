package remote

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

func (b backend) ListCollectionTemplates(context.Context) ([]mcp.CollectionTemplate, error) {
	ts := b.svc.CollectionTemplates()
	out := make([]mcp.CollectionTemplate, 0, len(ts))
	for _, t := range ts {
		schema := jsonObject(t.Schema)
		samples := make([]mcp.TemplateSample, 0, len(t.Samples))
		for _, s := range t.Samples {
			samples = append(samples, mcp.TemplateSample{ID: s.ID, Position: s.Position, Data: jsonObject(s.Data)})
		}
		out = append(out, mcp.CollectionTemplate{Name: t.Name, Title: t.Title, Description: t.Description, Mode: t.Mode,
			Fields: mcp.TemplateFields(schema), Schema: schema, Samples: samples, Snippet: t.Snippet(t.Name)})
	}
	return out, nil
}
