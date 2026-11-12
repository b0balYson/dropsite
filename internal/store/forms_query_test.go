package store

import (
	"context"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func insertSub(t *testing.T, db *DB, site, form string, at time.Time, fields ...domain.FormField) int64 {
	t.Helper()
	id, err := db.InsertFormSubmission(context.Background(), domain.FormSubmission{SiteID: site, Host: "h", Form: form,
		Fields: fields, CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func values(subs []domain.FormSubmission) []string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.Fields[0].Value)
	}
	return out
}

func TestListFormSubmissionsPeriodAndText(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	mkSite(t, db, "site000002", u, "", t0)
	f := func(v string) domain.FormField { return domain.FormField{Name: "name", Value: v} }
	insertSub(t, db, "site000001", "lead", t0, f("Anna"))
	insertSub(t, db, "site000001", "lead", t0.Add(24*time.Hour), f("50% off"), domain.FormField{Name: "x_y", Value: "b"})
	insertSub(t, db, "site000001", "order", t0.Add(48*time.Hour), f("ANNA K"))
	insertSub(t, db, "site000002", "lead", t0.Add(48*time.Hour), f("Anna"))

	cases := []struct {
		name  string
		f     FormSubmissionFilter
		want  []string
		total int
	}{
		{"all", FormSubmissionFilter{}, []string{"ANNA K", "50% off", "Anna"}, 3},
		{"since", FormSubmissionFilter{Since: t0.Add(time.Hour)}, []string{"ANNA K", "50% off"}, 2},
		{"until exclusive", FormSubmissionFilter{Until: t0.Add(48 * time.Hour)}, []string{"50% off", "Anna"}, 2},
		{"text any case", FormSubmissionFilter{Query: "anna"}, []string{"ANNA K", "Anna"}, 2},
		{"percent is literal", FormSubmissionFilter{Query: "%"}, []string{"50% off"}, 1},
		{"underscore is literal", FormSubmissionFilter{Query: "_"}, []string{}, 0},
		{"names are not searched", FormSubmissionFilter{Query: "name"}, []string{}, 0},
		{"form and text", FormSubmissionFilter{Form: "lead", Query: "ann"}, []string{"Anna"}, 1},
	}
	for _, c := range cases {
		c.f.SiteID = "site000001"
		subs, total, err := db.ListFormSubmissions(ctx, c.f)
		if err != nil {
			t.Fatal(c.name, err)
		}
		if got := values(subs); total != c.total || len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) {
			t.Errorf("%s: %v (%d), want %v (%d)", c.name, got, total, c.want, c.total)
		}
	}
}

func TestListFormSubmissionsCursor(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	// Two submissions share a timestamp, so the cursor must also use the ID.
	submitN(t, db, "site000001", 4, t0)
	insertSub(t, db, "site000001", "lead", t0.Add(3*time.Second), domain.FormField{Name: "n", Value: "4"})

	var seen []string
	f := FormSubmissionFilter{SiteID: "site000001", Page: Page{Limit: 2}}
	for range 5 {
		subs, total, err := db.ListFormSubmissions(ctx, f)
		if err != nil || total != 5 {
			t.Fatal(total, err)
		}
		if len(subs) == 0 {
			break
		}
		seen = append(seen, values(subs)...)
		last := subs[len(subs)-1]
		f.After = &SubmissionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	want := "4 3 2 1 0"
	if got := joinVals(seen); got != want {
		t.Fatalf("pages %q, want %q", got, want)
	}
}

func joinVals(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

// Text search ignores case in any script, not only ASCII.
func TestListFormSubmissionsCyrillicCase(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "site000001", u, "", t0)
	f := func(v string) domain.FormField { return domain.FormField{Name: "name", Value: v} }
	insertSub(t, db, "site000001", "lead", t0, f("Анна Петрова"))
	insertSub(t, db, "site000001", "lead", t0.Add(time.Hour), f("ЁЛКА"))
	insertSub(t, db, "site000001", "lead", t0.Add(2*time.Hour), f("Борис"))
	for q, want := range map[string]string{"анна": "Анна Петрова", "ПЕТРОВ": "Анна Петрова", "ёлка": "ЁЛКА", "бОрИс": "Борис"} {
		subs, total, err := db.ListFormSubmissions(ctx, FormSubmissionFilter{SiteID: "site000001", Query: q})
		if err != nil {
			t.Fatal(q, err)
		}
		if total != 1 || len(subs) != 1 || subs[0].Fields[0].Value != want {
			t.Fatalf("%q: %v (%d)", q, values(subs), total)
		}
	}
}
