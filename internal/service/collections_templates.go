package service

import (
	"context"
	"strings"

	"github.com/b0balYson/dropsite/internal/datatpl"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// CollectionTemplates returns the built-in collection templates. They are
// static and the same for every user, so no rights are checked.
func (s *Service) CollectionTemplates() []datatpl.Template { return datatpl.All() }

// applyTemplate fills an empty title, mode and schema from the template the
// input names and returns that template (nil without one).
func applyTemplate(in CollectionInput) (CollectionInput, *datatpl.Template, error) {
	name := strings.TrimSpace(in.Template)
	if name == "" {
		if in.WithSamples {
			return in, nil, collectionErr("with_samples", "sample documents come from a template: set template too")
		}
		return in, nil, nil
	}
	t, ok := datatpl.Get(name)
	if !ok {
		return in, nil, domain.E(domain.KindInvalid, domain.CodeCollectionInvalid, "unknown template "+name+"; templates: "+
			strings.Join(datatpl.Names(), ", ")).WithParams(map[string]any{"field": "template", "templates": datatpl.Names()})
	}
	in.Template = t.Name
	if strings.TrimSpace(in.Title) == "" {
		in.Title = t.Title
	}
	if in.Mode == "" {
		in.Mode = domain.CollectionMode(t.Mode)
	}
	if s := strings.TrimSpace(string(in.Schema)); s == "" || s == "null" {
		in.Schema = t.Schema
	}
	return in, &t, nil
}

// writeSamples stores the template's sample documents in a new collection
// as an owner write: they are checked against the collection schema.
func (s *Service) writeSamples(ctx context.Context, c domain.Collection, t *datatpl.Template) error {
	docs := make([]DocumentInput, 0, len(t.Samples))
	for _, smp := range t.Samples {
		pos := smp.Position
		docs = append(docs, DocumentInput{ID: smp.ID, Data: smp.Data, Position: &pos})
	}
	in, err := s.prepareDocuments(c, docs)
	if err != nil {
		return err
	}
	return s.repo.SaveDocuments(ctx, store.DocumentsWrite{SiteID: c.SiteID, Collection: c.Name, Docs: in,
		MaxDocs: MaxDocumentsPerCollection, Now: s.now()})
}
