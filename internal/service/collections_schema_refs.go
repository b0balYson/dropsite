package service

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Reference loops. The validator follows $ref without consuming any part of
// the document, so a schema such as {"$ref": "#"} or two definitions that
// refer to each other recurse until the Go stack overflows, which kills the
// whole process. checkSchemaRefs rejects every schema in which a chain of
// in-place keywords ($ref, allOf, anyOf, oneOf, not, if/then/else,
// dependentSchemas, dependencies) can come back to where it started; a
// reference reached through properties, items and the like is fine, since
// each step there descends into the (bounded) document.

// inPlaceSingle and inPlaceList are the applicators that validate the same
// instance; consuming keywords descend into a part of it.
var (
	inPlaceSingle = []string{"not", "if", "then", "else"}
	inPlaceList   = []string{"allOf", "anyOf", "oneOf"}
	inPlaceMap    = []string{"dependentSchemas", "dependencies"}

	consumingSingle = []string{"items", "additionalItems", "additionalProperties", "contains", "propertyNames",
		"unevaluatedItems", "unevaluatedProperties"}
	consumingList = []string{"prefixItems", "items"}
	consumingMap  = []string{"properties", "patternProperties"}
	// definitionMaps hold schemas reached only through references.
	definitionMaps = []string{"$defs", "definitions"}
)

const (
	refUnvisited = iota
	refActive
	refDone
)

type refGraph struct {
	nodes   map[string]any    // JSON pointer → schema (object or boolean)
	anchors map[string]string // $anchor → JSON pointer
	state   map[string]int
}

func ptrJoin(base, key string) string {
	key = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	return base + "/" + key
}

// collect registers every subschema of node under its JSON pointer.
func (g *refGraph) collect(ptr string, node any) {
	g.nodes[ptr] = node
	obj, ok := node.(map[string]any)
	if !ok {
		return
	}
	if a, ok := obj["$anchor"].(string); ok {
		g.anchors[a] = ptr
	}
	for _, k := range append(append([]string{}, inPlaceSingle...), consumingSingle...) {
		if sub, ok := obj[k]; ok && isSchemaValue(sub) {
			g.collect(ptrJoin(ptr, k), sub)
		}
	}
	for _, k := range append(append([]string{}, inPlaceList...), consumingList...) {
		if arr, ok := obj[k].([]any); ok {
			for i, sub := range arr {
				if isSchemaValue(sub) {
					g.collect(ptrJoin(ptr, k)+"/"+strconv.Itoa(i), sub)
				}
			}
		}
	}
	for _, k := range append(append(append([]string{}, inPlaceMap...), consumingMap...), definitionMaps...) {
		if m, ok := obj[k].(map[string]any); ok {
			for name, sub := range m {
				if isSchemaValue(sub) {
					g.collect(ptrJoin(ptrJoin(ptr, k), name), sub)
				}
			}
		}
	}
}

func isSchemaValue(v any) bool {
	switch v.(type) {
	case map[string]any, bool:
		return true
	}
	return false
}

// target resolves a reference within the document; ok is false for any
// form this check does not understand.
func (g *refGraph) target(ref string) (string, bool) {
	frag, found := strings.CutPrefix(ref, "#")
	if !found {
		return "", false
	}
	frag, err := url.PathUnescape(frag)
	if err != nil {
		return "", false
	}
	if frag == "" {
		return "", true
	}
	if !strings.HasPrefix(frag, "/") {
		p, ok := g.anchors[frag]
		return p, ok
	}
	if _, ok := g.nodes[frag]; !ok {
		return "", false
	}
	return frag, true
}

// edges lists the subschemas validated against the same instance as ptr.
func (g *refGraph) edges(ptr string) ([]string, error) {
	obj, ok := g.nodes[ptr].(map[string]any)
	if !ok {
		return nil, nil
	}
	var out []string
	for _, k := range []string{"$dynamicRef", "$recursiveRef"} {
		if _, ok := obj[k]; ok {
			return nil, schemaErr(k + " is not supported in collection schemas")
		}
	}
	if ref, ok := obj["$ref"].(string); ok {
		t, ok := g.target(ref)
		if !ok {
			return nil, schemaErr("only references within the schema (#/... or #anchor) are supported: " + ref)
		}
		out = append(out, t)
	}
	for _, k := range inPlaceSingle {
		if _, ok := g.nodes[ptrJoin(ptr, k)]; ok {
			out = append(out, ptrJoin(ptr, k))
		}
	}
	for _, k := range inPlaceList {
		if arr, ok := obj[k].([]any); ok {
			for i := range arr {
				if p := ptrJoin(ptr, k) + "/" + strconv.Itoa(i); g.nodes[p] != nil {
					out = append(out, p)
				}
			}
		}
	}
	for _, k := range inPlaceMap {
		if m, ok := obj[k].(map[string]any); ok {
			for name := range m {
				if p := ptrJoin(ptrJoin(ptr, k), name); g.nodes[p] != nil {
					out = append(out, p)
				}
			}
		}
	}
	return out, nil
}

var errSchemaLoop = schemaErr("the schema refers to itself without descending into a property or an item; " +
	"such a reference never ends")

func (g *refGraph) visit(ptr string) error {
	switch g.state[ptr] {
	case refActive:
		return errSchemaLoop
	case refDone:
		return nil
	}
	g.state[ptr] = refActive
	next, err := g.edges(ptr)
	if err != nil {
		return err
	}
	for _, n := range next {
		if err := g.visit(n); err != nil {
			return err
		}
	}
	g.state[ptr] = refDone
	return nil
}

// checkSchemaRefs rejects reference loops and references this check cannot
// follow (other documents, $id-relative, dynamic references).
func checkSchemaRefs(compact []byte) error {
	var root any
	if err := json.Unmarshal(compact, &root); err != nil {
		return schemaErr("the schema is not valid JSON")
	}
	g := &refGraph{nodes: map[string]any{}, anchors: map[string]string{}, state: map[string]int{}}
	g.collect("", root)
	for ptr := range g.nodes {
		if obj, ok := g.nodes[ptr].(map[string]any); ok && ptr != "" {
			if _, ok := obj["$id"]; ok {
				return schemaErr("$id is allowed only at the top of a collection schema")
			}
		}
		if err := g.visit(ptr); err != nil {
			return err
		}
	}
	return nil
}
