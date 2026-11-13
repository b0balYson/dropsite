package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// Bounds of a sheet linked to a collection.
const (
	MaxSheetColumns   = 100
	MaxSheetRowErrors = 50
)

// sheetIDColumn names the column that holds document IDs.
const sheetIDColumn = "id"

// sheetRows is a sheet turned into documents, or the problems found.
type sheetRows struct {
	docs   []store.DocumentInput
	errs   []domain.SheetRowError
	failed int // rows with problems, beyond the reported ones too
}

func (r *sheetRows) fail(row int, field, msg string) {
	r.failed++
	if len(r.errs) < MaxSheetRowErrors {
		r.errs = append(r.errs, domain.SheetRowError{Row: row, Field: field, Message: msg})
	}
}

// schemaTypes maps the top-level properties of a schema to their JSON type
// (the first non-null one of a type list).
func schemaTypes(schema json.RawMessage) map[string]string {
	var s struct {
		Properties map[string]struct {
			Type json.RawMessage `json:"type"`
		} `json:"properties"`
	}
	out := map[string]string{}
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil {
		return out
	}
	for name, p := range s.Properties {
		var one string
		if json.Unmarshal(p.Type, &one) == nil {
			out[name] = one
			continue
		}
		var many []string
		if json.Unmarshal(p.Type, &many) == nil {
			for _, t := range many {
				if t != "null" {
					out[name] = t
					break
				}
			}
		}
	}
	return out
}

// sheetNumber reads "1 290,50", "1,290.50" or "12.5" as a number.
func sheetNumber(v string) (float64, bool) {
	v = strings.NewReplacer(" ", "", " ", "", " ", "", "'", "").Replace(v)
	switch {
	case strings.Contains(v, ",") && strings.Contains(v, "."):
		v = strings.ReplaceAll(v, ",", "")
	case strings.Count(v, ",") == 1:
		v = strings.Replace(v, ",", ".", 1)
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// sheetValue converts a displayed cell value to the schema type; a value
// that does not convert stays a string, so the schema check reports it.
func sheetValue(typ, v string) any {
	switch typ {
	case "number":
		if f, ok := sheetNumber(v); ok {
			return f
		}
	case "integer":
		if f, ok := sheetNumber(v); ok && f == math.Trunc(f) && math.Abs(f) < 1<<53 {
			return int64(f)
		}
	case "boolean":
		switch strings.ToLower(v) {
		case "true", "yes", "да", "1", "истина", "是", "真":
			return true
		case "false", "no", "нет", "0", "ложь", "否", "假":
			return false
		}
	case "array":
		out := []any{}
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return v
}

func blankRow(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// sheetHeader reads the field names of row 1; -1 marks a skipped column.
func sheetHeader(row []string, res *sheetRows) ([]string, int) {
	names := make([]string, len(row))
	idCol := -1
	seen := map[string]bool{}
	for i, h := range row {
		h = strings.TrimSpace(h)
		names[i] = h
		if h == "" {
			continue
		}
		if seen[h] {
			res.fail(1, h, fmt.Sprintf("the column name %q is used twice", h))
			continue
		}
		seen[h] = true
		if strings.EqualFold(h, sheetIDColumn) && idCol < 0 {
			idCol = i
		}
	}
	return names, idCol
}

// sheetDocuments turns the rows of a sheet into documents of c: row 1 holds
// the field names, every other non-blank row is a document.
func sheetDocuments(c domain.Collection, rs *jsonschema.Resolved, rows [][]string) sheetRows {
	var res sheetRows
	for len(rows) > 0 && blankRow(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 || blankRow(rows[0]) {
		res.fail(1, "", "the first row must hold the field names, e.g. name, price, description")
		return res
	}
	header, idCol := sheetHeader(rows[0], &res)
	if len(header) > MaxSheetColumns {
		res.fail(1, "", fmt.Sprintf("the sheet has more than %d columns", MaxSheetColumns))
	}
	if len(rows)-1 > MaxDocumentsPerCollection {
		res.fail(0, "", fmt.Sprintf("the sheet has more than %d rows; a collection holds at most %d documents",
			MaxDocumentsPerCollection, MaxDocumentsPerCollection))
	}
	if res.failed > 0 {
		return res
	}
	types := schemaTypes(c.Schema)
	ids := map[string]int{}
	for i, r := range rows[1:] {
		rowNum := i + 2
		if blankRow(r) {
			continue
		}
		obj := map[string]any{}
		id := "row-" + strconv.Itoa(rowNum)
		bad := false
		for col, cell := range r {
			if col >= len(header) || header[col] == "" {
				continue
			}
			v := strings.TrimSpace(cell)
			if v == "" {
				continue
			}
			if col == idCol {
				n, ok := domain.NormalizeDocumentID(v)
				if !ok {
					res.fail(rowNum, header[col], "the id must be 1-64 characters a-z, 0-9, - and _, starting with a letter or digit")
					bad = true
					continue
				}
				id = n
				continue
			}
			obj[header[col]] = sheetValue(types[header[col]], v)
		}
		if prev, dup := ids[id]; dup {
			res.fail(rowNum, sheetIDColumn, fmt.Sprintf("the id %s is already used in row %d", id, prev))
			continue
		}
		ids[id] = rowNum
		if bad {
			continue
		}
		data, err := checkSheetDocument(rs, obj)
		if err != nil {
			de := domain.AsError(err)
			field, _ := de.Params["field"].(string)
			res.fail(rowNum, field, de.Message)
			continue
		}
		pos := int64(rowNum)
		res.docs = append(res.docs, store.DocumentInput{ID: id, Data: data, Position: &pos})
	}
	return res
}

// checkSheetDocument applies the size limit and the collection schema.
func checkSheetDocument(rs *jsonschema.Resolved, obj map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, docErr(-1, "", "the row cannot be stored")
	}
	data, decoded, err := compactObject(raw, MaxDocumentBytes, -1)
	if err != nil {
		return nil, err
	}
	if err := validateDocument(rs, decoded, -1); err != nil {
		return nil, err
	}
	return data, nil
}
