package service

import (
	"encoding/json"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// A reference loop used to overflow the validator's stack and kill the
// process on the first document write; such schemas are now refused.
func TestParseSchemaRejectsReferenceLoops(t *testing.T) {
	loops := []string{
		`{"type":"object","$ref":"#"}`,
		`{"type":"object","allOf":[{"$ref":"#"}]}`,
		`{"type":"object","$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`,
		`{"type":"object","properties":{"a":{"anyOf":[{"$ref":"#/properties/a"}]}}}`,
		`{"type":"object","$defs":{"x":{"$anchor":"x","not":{"$ref":"#x"}}},"properties":{"a":{"$ref":"#x"}}}`,
		`{"type":"object","definitions":{"a":{"$ref":"#/definitions/a"}}}`,
	}
	for _, s := range loops {
		_, _, err := parseSchema(json.RawMessage(s))
		if domain.AsError(err).Code != domain.CodeCollectionInvalid {
			t.Errorf("%s: want collection_invalid, got %v", s, err)
		}
	}
}

func TestParseSchemaAcceptsDescendingReferences(t *testing.T) {
	ok := []string{
		`{"type":"object","properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#"}}}}`,
		`{"type":"object","$defs":{"tag":{"type":"string"}},"properties":{"tags":{"type":"array","items":{"$ref":"#/$defs/tag"}}}}`,
		`{"type":"object","$defs":{"n":{"$anchor":"n","type":"object","properties":{"next":{"$ref":"#n"}}}},"properties":{"head":{"$ref":"#n"}}}`,
		`{"type":"object","allOf":[{"$ref":"#/$defs/base"}],"$defs":{"base":{"properties":{"a":{"type":"string"}}}}}`,
	}
	for _, s := range ok {
		_, rs, err := parseSchema(json.RawMessage(s))
		if err != nil {
			t.Errorf("%s: %v", s, err)
			continue
		}
		if err := validateDocument(rs, map[string]any{"name": "x", "children": []any{map[string]any{"name": "y"}}}, 0); err != nil {
			t.Errorf("%s: validate: %v", s, err)
		}
	}
}

func TestParseSchemaRejectsUnfollowableReferences(t *testing.T) {
	for _, s := range []string{
		`{"type":"object","properties":{"a":{"$dynamicRef":"#meta"}}}`,
		`{"type":"object","properties":{"a":{"$id":"https://example.com/a","type":"string"}}}`,
	} {
		if _, _, err := parseSchema(json.RawMessage(s)); err == nil {
			t.Errorf("%s: accepted", s)
		}
	}
}
