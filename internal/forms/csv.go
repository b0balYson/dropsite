package forms

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// CSV export limits. Field names are chosen by visitors, so the number of
// distinct names across an export is unbounded; only the most frequent
// names get their own column and the rest go to OtherColumn as JSON.
const (
	// MaxCSVColumns is the number of field-name columns in an export.
	MaxCSVColumns = 100
	// maxTrackedNames bounds the memory spent counting distinct names.
	maxTrackedNames = 10000
	// OtherColumn holds fields without a column of their own as a JSON
	// object. Names starting with "_" are dropped at intake, so it cannot
	// collide with a real field.
	OtherColumn = "_other"
)

// csvRisky are leading characters that spreadsheets interpret as formulas.
const csvRisky = "=+-@\t\r"

func csvCell(s string) string {
	if s != "" && strings.ContainsRune(csvRisky, rune(s[0])) {
		return "'" + s
	}
	return s
}

// ColumnPicker counts field names over a first pass of an export.
type ColumnPicker struct {
	counts   map[string]int
	order    []string
	overflow bool
}

// NewColumnPicker returns an empty picker.
func NewColumnPicker() *ColumnPicker { return &ColumnPicker{counts: map[string]int{}} }

// Add counts the field names of one submission.
func (p *ColumnPicker) Add(fields []domain.FormField) {
	for _, f := range fields {
		if _, ok := p.counts[f.Name]; ok {
			p.counts[f.Name]++
			continue
		}
		if len(p.order) >= maxTrackedNames {
			p.overflow = true
			continue
		}
		p.counts[f.Name] = 1
		p.order = append(p.order, f.Name)
	}
}

// Columns returns up to max of the most frequent names (ties: first seen),
// in first-seen order, and whether some names were left without a column.
func (p *ColumnPicker) Columns(limit int) ([]string, bool) {
	if len(p.order) <= limit {
		return append([]string(nil), p.order...), p.overflow
	}
	rank := make([]int, len(p.order))
	for i := range rank {
		rank[i] = i
	}
	sort.SliceStable(rank, func(a, b int) bool { return p.counts[p.order[rank[a]]] > p.counts[p.order[rank[b]]] })
	chosen := rank[:limit]
	sort.Ints(chosen)
	out := make([]string, len(chosen))
	for i, r := range chosen {
		out[i] = p.order[r]
	}
	return out, true
}

// CSVWriter streams submissions as CSV: created_at, form, host, one column
// per chosen field name and, if needed, OtherColumn.
type CSVWriter struct {
	cw    *csv.Writer
	cols  []string
	index map[string]int
	other bool
}

// NewCSVWriter writes the header row.
func NewCSVWriter(w io.Writer, cols []string, other bool) (*CSVWriter, error) {
	c := &CSVWriter{cw: csv.NewWriter(w), cols: cols, index: make(map[string]int, len(cols)), other: other}
	header := []string{"created_at", "form", "host"}
	for i, n := range cols {
		c.index[n] = i
		header = append(header, csvCell(n))
	}
	if other {
		header = append(header, OtherColumn)
	}
	return c, c.cw.Write(header)
}

// Write writes one submission.
func (c *CSVWriter) Write(s domain.FormSubmission) error {
	row := make([]string, 3+len(c.cols), 4+len(c.cols))
	row[0], row[1], row[2] = s.CreatedAt.UTC().Format(time.RFC3339), s.Form, s.Host
	var rest map[string]string
	for _, f := range s.Fields {
		if i, ok := c.index[f.Name]; ok {
			row[3+i] = csvCell(f.Value)
			continue
		}
		if rest == nil {
			rest = map[string]string{}
		}
		rest[f.Name] = f.Value
	}
	if c.other {
		cell := ""
		if len(rest) > 0 {
			b, err := json.Marshal(rest)
			if err != nil {
				return err
			}
			cell = string(b)
		}
		row = append(row, csvCell(cell))
	}
	return c.cw.Write(row)
}

// Flush writes buffered rows to the underlying writer.
func (c *CSVWriter) Flush() error {
	c.cw.Flush()
	return c.cw.Error()
}

// WriteCSV writes subs as CSV in one go (small exports and tests).
func WriteCSV(w io.Writer, subs []domain.FormSubmission) error {
	p := NewColumnPicker()
	for _, s := range subs {
		p.Add(s.Fields)
	}
	cols, other := p.Columns(MaxCSVColumns)
	cw, err := NewCSVWriter(w, cols, other)
	if err != nil {
		return err
	}
	for _, s := range subs {
		if err := cw.Write(s); err != nil {
			return err
		}
	}
	return cw.Flush()
}
