package mcp

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// withEnums returns the input schema of In with the allowed values of the
// named string properties enumerated, so a model sees every choice in the
// schema and not only in the description. An optional property whose empty
// value means "the default" or "keep" lists "" too, so that a client that
// sends every field is not refused.
func withEnums[In any](enums map[string][]string) *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(err)
	}
	for name, values := range enums {
		p, ok := s.Properties[name]
		if !ok {
			panic(fmt.Sprintf("mcp: %T has no property %q", *new(In), name))
		}
		for _, v := range values {
			p.Enum = append(p.Enum, v)
		}
	}
	return s
}
