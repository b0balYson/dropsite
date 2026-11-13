package api

import (
	"encoding/json"
	"net/http"

	"github.com/b0balYson/dropsite/internal/datatpl"
)

type templateSampleDTO struct {
	ID       string          `json:"id"`
	Position int64           `json:"position"`
	Data     json.RawMessage `json:"data"`
}

type collectionTemplateDTO struct {
	Name        string              `json:"name"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Mode        string              `json:"mode"`
	Schema      json.RawMessage     `json:"schema"`
	Samples     []templateSampleDTO `json:"samples"`
	// Snippet is for a collection named like the template; replace the
	// name for another collection.
	Snippet string `json:"snippet"`
}

func toTemplate(t datatpl.Template) collectionTemplateDTO {
	samples := make([]templateSampleDTO, 0, len(t.Samples))
	for _, s := range t.Samples {
		samples = append(samples, templateSampleDTO(s))
	}
	return collectionTemplateDTO{Name: t.Name, Title: t.Title, Description: t.Description, Mode: t.Mode,
		Schema: t.Schema, Samples: samples, Snippet: t.Snippet(t.Name)}
}

func (a *api) listCollectionTemplates(w http.ResponseWriter, _ *http.Request) error {
	ts := a.svc.CollectionTemplates()
	items := make([]collectionTemplateDTO, 0, len(ts))
	for _, t := range ts {
		items = append(items, toTemplate(t))
	}
	writeJSON(w, http.StatusOK, newList(items, len(items)))
	return nil
}
