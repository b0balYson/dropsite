package forms

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestCSVCapsColumnsAndKeepsTheRestAsJSON(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	var subs []domain.FormSubmission
	// 300 submissions with 50 unique names each, plus a common "email".
	for i := range 300 {
		fields := []domain.FormField{{Name: "email", Value: fmt.Sprintf("u%d@x.test", i)}}
		for j := range 49 {
			fields = append(fields, domain.FormField{Name: fmt.Sprintf("f%d_%d", i, j), Value: "v"})
		}
		subs = append(subs, domain.FormSubmission{Form: "lead", Host: "a.test", CreatedAt: at, Fields: fields})
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, subs); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	header := rows[0]
	if len(header) != 3+MaxCSVColumns+1 || header[3] != "email" || header[len(header)-1] != OtherColumn {
		t.Fatalf("header has %d columns: %v…", len(header), header[:5])
	}
	last := rows[len(rows)-1]
	if last[3] != "u299@x.test" || !strings.HasPrefix(last[len(last)-1], `{"f299_`) {
		t.Fatalf("row %v", last[len(last)-1])
	}
}

func TestColumnPickerPrefersFrequentNames(t *testing.T) {
	p := NewColumnPicker()
	p.Add([]domain.FormField{{Name: "rare"}, {Name: "name"}})
	p.Add([]domain.FormField{{Name: "name"}, {Name: "phone"}})
	p.Add([]domain.FormField{{Name: "phone"}})
	cols, other := p.Columns(2)
	if !other || strings.Join(cols, ",") != "name,phone" {
		t.Fatalf("cols %v other %v", cols, other)
	}
	if cols, other := p.Columns(10); other || len(cols) != 3 {
		t.Fatalf("all cols %v %v", cols, other)
	}
}

func TestColumnPickerBoundsTrackedNames(t *testing.T) {
	p := NewColumnPicker()
	for i := range maxTrackedNames + 50 {
		p.Add([]domain.FormField{{Name: fmt.Sprint("n", i)}})
	}
	if len(p.counts) != maxTrackedNames {
		t.Fatalf("tracked %d names", len(p.counts))
	}
	if _, other := p.Columns(maxTrackedNames); !other {
		t.Fatal("untracked names must go to the other column")
	}
}
