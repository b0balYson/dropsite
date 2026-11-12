package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// formExportPage is the number of rows read per database round trip.
const formExportPage = 200

// formExportMaxBytes caps the size of one export (a variable for tests).
var formExportMaxBytes int64 = 64 << 20

// ExportFormSubmissions streams up to formExportLimit newest submissions as
// CSV. Access is checked first; start is then called once with the site ID
// (for the file name) and returns the writer, so an adapter sends headers
// only after the request is known to succeed. An error returned after start
// means the CSV is truncated; the adapter can only log it. The export
// stops at a row boundary once formExportMaxBytes are written.
func (s *Service) ExportFormSubmissions(ctx context.Context, a Actor, ref, form string, start func(siteID string) io.Writer) error {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return err
	}
	// First pass: choose the columns and fix the set of rows.
	picker := forms.NewColumnPicker()
	var top int64
	rows := 0
	err = s.eachSubmission(ctx, site.ID, form, math.MaxInt64, formExportLimit, func(sub domain.FormSubmission) error {
		if top == 0 {
			top = sub.ID
		}
		picker.Add(sub.Fields)
		rows++
		return nil
	})
	if err != nil {
		return err
	}
	cols, other := picker.Columns(forms.MaxCSVColumns)
	cw := &countingWriter{w: start(site.ID)}
	csvw, err := forms.NewCSVWriter(cw, cols, other)
	if err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	if rows > 0 {
		err = s.eachSubmission(ctx, site.ID, form, top+1, rows, func(sub domain.FormSubmission) error {
			if cw.n >= formExportMaxBytes {
				return errExportFull
			}
			if err := csvw.Write(sub); err != nil {
				return fmt.Errorf("write csv: %w", err)
			}
			return nil
		})
	}
	if errors.Is(err, errExportFull) {
		s.log.Warn("form export truncated at the size limit", "site", site.ID, "bytes", cw.n)
		err = nil
	}
	if err != nil {
		return err
	}
	if err := csvw.Flush(); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return nil
}

var errExportFull = errors.New("export size limit reached")

// eachSubmission calls fn for up to limit submissions with ID below before,
// newest first, reading formExportPage rows at a time.
func (s *Service) eachSubmission(ctx context.Context, siteID, form string, before int64, limit int, fn func(domain.FormSubmission) error) error {
	seen := 0
	for seen < limit {
		page, err := s.repo.ListSubmissionsBefore(ctx, siteID, form, before, min(formExportPage, limit-seen))
		if err != nil {
			return err
		}
		for _, sub := range page {
			if err := fn(sub); err != nil {
				return err
			}
			before = sub.ID
		}
		seen += len(page)
		if len(page) < formExportPage {
			return nil
		}
	}
	return nil
}

// countingWriter counts bytes passed to w.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
