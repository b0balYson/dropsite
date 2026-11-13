package ui

import (
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// templateInput turns the create form into the service input. With a
// template chosen, the template's mode and schema are used (the mode radio
// always has a value, so it cannot mean "keep the template's"); both can
// be changed later in the collection settings. Samples need a template.
func templateInput(f collectionForm) service.CollectionInput {
	in := service.CollectionInput{Name: f.Name, Title: f.Title, Mode: domain.CollectionMode(f.Mode), Schema: schemaText(f.Schema)}
	if t := strings.TrimSpace(f.Template); t != "" {
		in.Template, in.Mode, in.Schema = t, "", nil
		in.WithSamples = f.WithSamples
	}
	return in
}
