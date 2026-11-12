package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/b0balYson/dropsite/internal/domain"
)

// maxCachedSchemas bounds the resolved schema cache; past it the cache is
// dropped and refilled on demand.
const maxCachedSchemas = 2000

// schemaCache keeps resolved collection schemas keyed by site, name and the
// collection's updated_at, so a changed schema is never served stale.
type schemaCache struct {
	mu      sync.Mutex
	entries map[string]*jsonschema.Resolved
}

func schemaKey(c domain.Collection) string {
	return c.SiteID + "/" + c.Name + "/" + c.UpdatedAt.Format(time.RFC3339Nano)
}

func (sc *schemaCache) get(c domain.Collection) (*jsonschema.Resolved, error) {
	if len(c.Schema) == 0 {
		return nil, nil
	}
	key := schemaKey(c)
	sc.mu.Lock()
	rs, ok := sc.entries[key]
	sc.mu.Unlock()
	if ok {
		return rs, nil
	}
	_, rs, err := parseSchema(c.Schema)
	if err != nil {
		return nil, err
	}
	sc.mu.Lock()
	if sc.entries == nil || len(sc.entries) >= maxCachedSchemas {
		sc.entries = map[string]*jsonschema.Resolved{}
	}
	sc.entries[key] = rs
	sc.mu.Unlock()
	return rs, nil
}

func schemaErr(msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeCollectionInvalid, msg).WithParams(map[string]any{"field": "schema"})
}

// parseSchema checks a collection schema: a JSON Schema (draft-07 or 2020-12)
// of an object, at most MaxSchemaBytes, without remote references. It
// returns the compact text and the resolved schema.
func parseSchema(raw json.RawMessage) (json.RawMessage, *jsonschema.Resolved, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, nil, schemaErr("the schema is not valid JSON")
	}
	if buf.Len() > MaxSchemaBytes {
		return nil, nil, schemaErr("the schema is larger than 16 KB")
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		return nil, nil, schemaErr("the schema is not a JSON Schema object: " + err.Error())
	}
	if s.Type != "object" {
		return nil, nil, schemaErr(`the schema must describe an object: set "type": "object"`)
	}
	switch s.Schema {
	case "", "http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2020-12/schema":
	default:
		return nil, nil, schemaErr("only JSON Schema draft-07 and 2020-12 are supported")
	}
	rs, err := s.Resolve(nil)
	if err != nil {
		return nil, nil, schemaErr("the schema cannot be used: " + err.Error())
	}
	return buf.Bytes(), rs, nil
}

// schemaProperties returns the property names a schema declares.
func schemaProperties(raw json.RawMessage) map[string]bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(raw, &s)
	out := make(map[string]bool, len(s.Properties))
	for k := range s.Properties {
		out[k] = true
	}
	return out
}

var (
	validatingRe = regexp.MustCompile(`validating (/[^:]*):`)
	additionalRe = regexp.MustCompile(`additional properties \["([^"]*)"`)
	requiredRe   = regexp.MustCompile(`missing properties: \["([^"]*)"`)
)

// fieldPath turns a schema validation error into the instance field path
// (e.g. "tags" or "address/city") and a short message.
func fieldPath(err error) (string, string) {
	msg := err.Error()
	field := ""
	if ms := validatingRe.FindAllStringSubmatch(msg, -1); len(ms) > 0 {
		var parts []string
		segs := strings.Split(ms[len(ms)-1][1], "/")
		for i := 0; i+1 < len(segs); i++ {
			if segs[i] == "properties" {
				parts = append(parts, segs[i+1])
				i++
			}
		}
		field = strings.Join(parts, "/")
	}
	extra := ""
	if m := additionalRe.FindStringSubmatch(msg); m != nil {
		extra = m[1]
	} else if m := requiredRe.FindStringSubmatch(msg); m != nil {
		extra = m[1]
	}
	if extra != "" {
		if field != "" {
			field += "/"
		}
		field += extra
	}
	// Keep only the innermost reason: "validating root: validating
	// /properties/a: type: ..." → "type: ...".
	for {
		i := strings.Index(msg, "validating ")
		if i < 0 {
			break
		}
		j := strings.Index(msg[i:], ": ")
		if j < 0 {
			break
		}
		msg = msg[i+j+2:]
	}
	return field, msg
}

// docErr is a document_invalid error; index < 0 means a visitor document.
func docErr(index int, field, msg string) error {
	params := map[string]any{"field": field}
	if index >= 0 {
		params["index"] = index
	}
	return domain.E(domain.KindInvalid, domain.CodeDocumentInvalid, msg).WithParams(params)
}

// compactObject checks that raw is a JSON object of at most limit bytes in
// compact form and returns it compacted and decoded.
func compactObject(raw json.RawMessage, limit, index int) (json.RawMessage, map[string]any, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, nil, docErr(index, "", "the document is not valid JSON")
	}
	var obj map[string]any
	if !bytes.HasPrefix(buf.Bytes(), []byte("{")) || json.Unmarshal(buf.Bytes(), &obj) != nil {
		return nil, nil, docErr(index, "", "the document must be a JSON object")
	}
	if buf.Len() > limit {
		params := map[string]any{"field": "", "limit_bytes": limit}
		if index >= 0 {
			params["index"] = index
		}
		return nil, nil, domain.E(domain.KindInvalid, domain.CodeDocumentInvalid,
			fmt.Sprintf("the document is larger than %d KB", limit>>10)).WithParams(params)
	}
	return buf.Bytes(), obj, nil
}

// validateDocument checks obj against the resolved schema, if any.
func validateDocument(rs *jsonschema.Resolved, obj map[string]any, index int) error {
	if rs == nil {
		return nil
	}
	if err := rs.Validate(obj); err != nil {
		field, msg := fieldPath(err)
		if field != "" {
			msg = field + ": " + msg
		}
		return docErr(index, field, "the document does not match the collection schema: "+msg)
	}
	return nil
}
