package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func storeSubs(t *testing.T, e *env, siteID string, n int, at time.Time, value func(i int) string) {
	t.Helper()
	for i := range n {
		_, err := e.repo.InsertFormSubmission(context.Background(), domain.FormSubmission{SiteID: siteID, Host: "h", Form: "lead",
			Fields: []domain.FormField{{Name: "name", Value: value(i)}}, CreatedAt: at.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchFormSubmissionsPagesAndFilters(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	now := e.clock.Now()
	storeSubs(t, e, v.Site.ID, 5, now.Add(-10*24*time.Hour), func(i int) string { return fmt.Sprint("old", i) })
	storeSubs(t, e, v.Site.ID, 5, now.Add(-time.Hour), func(i int) string { return fmt.Sprint("new", i) })

	var got []string
	q := SubmissionQuery{Limit: 4}
	for {
		p, err := e.svc.SearchFormSubmissions(ctx, ann, v.Site.ID, q)
		if err != nil || p.Total != 10 {
			t.Fatal(p.Total, err)
		}
		for _, s := range p.Items {
			got = append(got, s.Fields[0].Value)
		}
		if p.Next == "" {
			break
		}
		q.Cursor = p.Next
	}
	if strings.Join(got, " ") != "new4 new3 new2 new1 new0 old4 old3 old2 old1 old0" {
		t.Fatalf("pages: %v", got)
	}
	p, err := e.svc.SearchFormSubmissions(ctx, ann, v.Site.ID, SubmissionQuery{Since: now.Add(-7 * 24 * time.Hour), Text: "NEW3"})
	if err != nil || p.Total != 1 || p.Items[0].Fields[0].Value != "new3" || p.Next != "" {
		t.Fatalf("filtered: %+v %v", p, err)
	}
	for _, bad := range []SubmissionQuery{
		{Cursor: "garbage"}, {Cursor: "1.x"}, {Form: "../x"}, {Text: strings.Repeat("a", MaxSubmissionQueryLen+1)},
		{Since: now, Until: now.Add(-time.Hour)},
	} {
		_, err := e.svc.SearchFormSubmissions(ctx, ann, v.Site.ID, bad)
		wantCode(t, err, domain.CodeInvalidInput)
	}
	bob := e.user("bob@pp.test")
	_, err = e.svc.SearchFormSubmissions(ctx, bob, v.Site.ID, SubmissionQuery{})
	wantNotFound(t, err)
	_, err = e.svc.ExportFormSubmissionsText(ctx, bob, v.Site.ID, SubmissionQuery{}, 10, 1<<20)
	wantNotFound(t, err)
}

func TestExportFormSubmissionsTextCaps(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	storeSubs(t, e, v.Site.ID, 30, e.clock.Now().Add(-time.Hour), func(i int) string {
		if i == 0 {
			return "=1+1"
		}
		return fmt.Sprint("row", i)
	})

	out, err := e.svc.ExportFormSubmissionsText(ctx, ann, v.Site.ID, SubmissionQuery{}, 0, 0)
	if err != nil || out.Rows != 30 || out.Total != 30 || out.Truncated {
		t.Fatalf("all: %+v %v", out, err)
	}
	lines := strings.Split(strings.TrimSpace(out.CSV), "\n")
	if len(lines) != 31 || !strings.HasPrefix(lines[0], "created_at,form,host,name") ||
		!strings.Contains(lines[30], "'=1+1") || !strings.Contains(lines[1], "row29") {
		t.Fatalf("csv: %q", out.CSV)
	}

	out, _ = e.svc.ExportFormSubmissionsText(ctx, ann, v.Site.ID, SubmissionQuery{}, 5, 0)
	if out.Rows != 5 || !out.Truncated || strings.Count(out.CSV, "\n") != 6 {
		t.Fatalf("rows cap: %+v", out)
	}
	// A byte cap cuts at a row boundary.
	out, _ = e.svc.ExportFormSubmissionsText(ctx, ann, v.Site.ID, SubmissionQuery{}, 0, 1)
	if out.Rows != 29 || !out.Truncated || !strings.HasSuffix(out.CSV, "\n") || len(out.CSV) > minInlineExportBytes {
		t.Fatalf("bytes cap: %+v", out)
	}
	subs, _, _ := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	csv, rows, err := boundedCSV(subs, len(lines[0])+1+len(lines[1])+1+10)
	if err != nil || rows != 1 || csv != lines[0]+"\n"+lines[1]+"\n" {
		t.Fatalf("row boundary: %d %q %v", rows, csv, err)
	}
	// Caps above the service maximum are clamped.
	out, _ = e.svc.ExportFormSubmissionsText(ctx, ann, v.Site.ID, SubmissionQuery{}, MaxInlineExportRows*10, MaxInlineExportBytes*10)
	if out.Rows != 30 {
		t.Fatalf("clamped: %+v", out)
	}
	if u := e.svc.FormsPageURL(v.Site.ID); !strings.HasSuffix(u, "/sites/"+v.Site.ID+"/forms") || !strings.HasPrefix(u, "http") {
		t.Fatalf("forms page url %q", u)
	}
}
