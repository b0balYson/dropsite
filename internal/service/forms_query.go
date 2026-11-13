package service

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/store"
)

// Limits of submission search and of exports returned inline (to chat
// assistants); the streaming export of the web interface is separate.
const (
	MaxSubmissionQueryLen    = 200
	DefaultSubmissionPage    = 20
	MaxSubmissionPage        = 1000
	DefaultInlineExportRows  = 200
	MaxInlineExportRows      = 100_000
	DefaultInlineExportBytes = 100 << 10
	MaxInlineExportBytes     = 16 << 20
	minInlineExportBytes     = 1 << 10
)

// SubmissionQuery filters submissions; zero values mean "no filter".
type SubmissionQuery struct {
	Form  string
	Text  string
	Since time.Time // inclusive
	Until time.Time // exclusive
	// Cursor is Next of the previous page.
	Cursor string
	Limit  int
}

// SubmissionPage is one page of a search, newest first.
type SubmissionPage struct {
	Items []domain.FormSubmission
	// Total counts every submission matching the filter.
	Total int
	// Next continues the listing; empty on the last page.
	Next string
}

// CSVExport is a small CSV export held in memory.
type CSVExport struct {
	CSV       string
	Rows      int
	Total     int
	Truncated bool
}

func invalidQuery(msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeInvalidInput, msg)
}

func encodeCursor(s domain.FormSubmission) string {
	return strconv.FormatInt(s.CreatedAt.UnixMilli(), 10) + "." + strconv.FormatInt(s.ID, 10)
}

func decodeCursor(c string) (*store.SubmissionCursor, error) {
	if c == "" {
		return nil, nil
	}
	ms, id, ok := strings.Cut(c, ".")
	at, err1 := strconv.ParseInt(ms, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if !ok || err1 != nil || err2 != nil || n <= 0 {
		return nil, invalidQuery("the cursor is not valid; list again without it")
	}
	return &store.SubmissionCursor{CreatedAt: time.UnixMilli(at).UTC(), ID: n}, nil
}

// submissionFilter validates q into a store filter for siteID.
func submissionFilter(siteID string, q SubmissionQuery) (store.FormSubmissionFilter, error) {
	f := store.FormSubmissionFilter{SiteID: siteID, Since: q.Since, Until: q.Until, Query: strings.TrimSpace(q.Text)}
	if q.Form != "" {
		name, ok := forms.NormalizeFormName(q.Form)
		if !ok {
			return f, invalidQuery("a form name has 1-64 characters a-z, 0-9, - and _")
		}
		f.Form = name
	}
	if utf8.RuneCountInString(f.Query) > MaxSubmissionQueryLen {
		return f, invalidQuery(fmt.Sprintf("the search text is longer than %d characters", MaxSubmissionQueryLen))
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && !q.Since.Before(q.Until) {
		return f, invalidQuery("the start of the period must be before its end")
	}
	after, err := decodeCursor(q.Cursor)
	if err != nil {
		return f, err
	}
	f.After = after
	return f, nil
}

// SearchFormSubmissions returns a page of the site's submissions matching q,
// newest first, continued by an opaque cursor.
func (s *Service) SearchFormSubmissions(ctx context.Context, a Actor, ref string, q SubmissionQuery) (SubmissionPage, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return SubmissionPage{}, err
	}
	f, err := submissionFilter(site.ID, q)
	if err != nil {
		return SubmissionPage{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSubmissionPage
	}
	limit = min(limit, MaxSubmissionPage)
	f.Limit = limit + 1 // one more tells whether a next page exists
	subs, total, err := s.repo.ListFormSubmissions(ctx, f)
	if err != nil {
		return SubmissionPage{}, err
	}
	p := SubmissionPage{Items: subs, Total: total}
	if len(subs) > limit {
		p.Items = subs[:limit]
		p.Next = encodeCursor(p.Items[limit-1])
	}
	return p, nil
}

// ExportFormSubmissionsText returns up to maxRows newest submissions matching
// q (cursor ignored) as CSV in the format of the streaming export, at most
// maxBytes long and cut at a row boundary. Zero caps take the defaults;
// caps are clamped to MaxInlineExportRows and MaxInlineExportBytes.
func (s *Service) ExportFormSubmissionsText(ctx context.Context, a Actor, ref string, q SubmissionQuery, maxRows, maxBytes int) (CSVExport, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return CSVExport{}, err
	}
	q.Cursor = ""
	f, err := submissionFilter(site.ID, q)
	if err != nil {
		return CSVExport{}, err
	}
	maxRows = clampCap(maxRows, DefaultInlineExportRows, 1, MaxInlineExportRows)
	maxBytes = clampCap(maxBytes, DefaultInlineExportBytes, minInlineExportBytes, MaxInlineExportBytes)
	subs, total, err := s.collectSubmissions(ctx, f, maxRows, maxBytes)
	if err != nil {
		return CSVExport{}, err
	}
	csv, rows, err := boundedCSV(subs, maxBytes)
	if err != nil {
		return CSVExport{}, err
	}
	return CSVExport{CSV: csv, Rows: rows, Total: total, Truncated: rows < total}, nil
}

func clampCap(v, def, lo, hi int) int {
	if v <= 0 {
		return def
	}
	return min(max(v, lo), hi)
}

// collectSubmissions reads up to limit submissions matching f, page by
// page. It stops early once the field values read pass budget bytes: every
// value is written to the CSV, so later rows could not fit anyway.
func (s *Service) collectSubmissions(ctx context.Context, f store.FormSubmissionFilter, limit, budget int) ([]domain.FormSubmission, int, error) {
	var out []domain.FormSubmission
	total, size := 0, 0
	for len(out) < limit && size <= budget {
		f.Limit = min(store.MaxPageSize, limit-len(out))
		page, n, err := s.repo.ListFormSubmissions(ctx, f)
		if err != nil {
			return nil, 0, err
		}
		total = n
		out = append(out, page...)
		for _, sub := range page {
			for _, fl := range sub.Fields {
				size += len(fl.Value)
			}
		}
		if len(page) < f.Limit {
			break
		}
		last := page[len(page)-1]
		f.After = &store.SubmissionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return out, total, nil
}

// boundedCSV writes subs as CSV until the next row would pass maxBytes.
func boundedCSV(subs []domain.FormSubmission, maxBytes int) (string, int, error) {
	picker := forms.NewColumnPicker()
	for _, sub := range subs {
		picker.Add(sub.Fields)
	}
	cols, other := picker.Columns(forms.MaxCSVColumns)
	var buf bytes.Buffer
	w, err := forms.NewCSVWriter(&buf, cols, other)
	if err == nil {
		err = w.Flush()
	}
	if err != nil {
		return "", 0, fmt.Errorf("write csv: %w", err)
	}
	rows := 0
	for _, sub := range subs {
		before := buf.Len()
		if err := w.Write(sub); err != nil {
			return "", 0, fmt.Errorf("write csv: %w", err)
		}
		if err := w.Flush(); err != nil {
			return "", 0, fmt.Errorf("write csv: %w", err)
		}
		if buf.Len() > maxBytes {
			buf.Truncate(before)
			break
		}
		rows++
	}
	return buf.String(), rows, nil
}

// FormsPageURL is the web interface screen with the site's submissions,
// channels and the full CSV export.
func (s *Service) FormsPageURL(siteID string) string {
	return s.cabinetBase() + "/sites/" + siteID + "/forms"
}
