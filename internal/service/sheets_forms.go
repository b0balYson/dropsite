package service

import (
	"context"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/sheets"
)

// The "sheets" form channel appends one row per submission. The first two
// columns are the time and the form; fields go to the columns with the same
// header, and a field without a column gets a new one on the right.

// Header names of the fixed columns per owner language; all of them are
// recognized so a language change does not add duplicate columns.
var (
	sheetDateHeaders = map[string]string{"ru": "Дата", "en": "Date", "zh": "日期"}
	sheetFormHeaders = map[string]string{"ru": "Форма", "en": "Form", "zh": "表单"}
	sheetDateNames   = headerNames(sheetDateHeaders)
	sheetFormNames   = headerNames(sheetFormHeaders)
)

// headerNames lists the names of one fixed column in every language.
func headerNames(byLang map[string]string) []string {
	names := make([]string, 0, len(byLang))
	for _, lang := range ownerLangs {
		names = append(names, byLang[lang])
	}
	return names
}

func headerIndex(header []string, names ...string) int {
	for i, h := range header {
		h = strings.TrimSpace(h)
		for _, n := range names {
			if h == n {
				return i
			}
		}
	}
	return -1
}

// trimHeader drops trailing empty cells of a header row.
func trimHeader(row []string) []string {
	n := len(row)
	for n > 0 && strings.TrimSpace(row[n-1]) == "" {
		n--
	}
	return append([]string(nil), row[:n]...)
}

// baseHeader adds the fixed columns missing from header.
func baseHeader(header []string, lang string) []string {
	if headerIndex(header, sheetDateNames...) < 0 {
		header = append(header, sheetDateHeaders[lang])
	}
	if headerIndex(header, sheetFormNames...) < 0 {
		header = append(header, sheetFormHeaders[lang])
	}
	return header
}

// prepareSheets checks a new "sheets" channel by writing the header row:
// a successful write is the confirmation, so the channel is active at once.
func (s *Service) prepareSheets(ctx context.Context, a Actor, ch *domain.FormChannel, target string) error {
	client, err := s.sheetsAPI(ctx)
	if err != nil {
		return err
	}
	ref, err := sheets.ParseURL(target)
	if err != nil {
		return s.sheetsCallErr(err, client.Email(), true)
	}
	ctx, cancel := context.WithTimeout(ctx, SheetCallTimeout)
	defer cancel()
	sh, err := client.Resolve(ctx, ref)
	if err != nil {
		return s.sheetsCallErr(err, client.Email(), true)
	}
	a1 := sheets.QuoteSheet(sh.Title)
	rows, err := client.Values(ctx, ref.SpreadsheetID, a1+"!1:1")
	if err != nil {
		return s.sheetsCallErr(err, client.Email(), true)
	}
	var header []string
	if len(rows) > 0 {
		header = trimHeader(rows[0])
	}
	header = baseHeader(header, ownerLang(a.User.Lang))
	if err := client.Update(ctx, ref.SpreadsheetID, a1+"!1:1", [][]string{header}); err != nil {
		return s.sheetsCallErr(err, client.Email(), true)
	}
	gid := sh.ID
	ch.Target = sheets.Ref{SpreadsheetID: ref.SpreadsheetID, GID: &gid}.URL()
	ch.Status = domain.ChannelActive
	return nil
}

// sheetRow lays out a submission along header, adding columns for new
// fields; it reports whether the header changed.
func sheetRow(header []string, m forms.Message) ([]string, []string, bool) {
	header = trimHeader(header)
	before := len(header)
	header = baseHeader(header, m.Lang)
	values := map[int]string{
		headerIndex(header, sheetDateNames...): m.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		headerIndex(header, sheetFormNames...): m.Form,
	}
	for _, f := range m.Fields {
		i := headerIndex(header, f.Name)
		if i < 0 {
			header = append(header, f.Name)
			i = len(header) - 1
		}
		values[i] = f.Value
	}
	row := make([]string, len(header))
	for i, v := range values {
		row[i] = v
	}
	return header, row, len(header) != before
}

// sendSheets appends a submission to the channel's sheet. Every failure is
// temporary: access may come back, and the circuit breaker pauses a
// channel that keeps failing.
func (s *Service) sendSheets(ctx context.Context, ch domain.FormChannel, m forms.Message) error {
	client, err := s.sheetsAPI(ctx)
	if err != nil {
		return forms.Temporary("Google Sheets is not configured on this installation")
	}
	ref, err := sheets.ParseURL(ch.Target)
	if err != nil {
		return forms.Permanent("the spreadsheet link of the channel is invalid; add the channel again")
	}
	fail := func(err error) error {
		s.log.Warn("google sheets delivery failed", "site", ch.SiteID, "channel", ch.ID, "err", sheets.Reason(err))
		if sheets.IsAccess(err) {
			return forms.Temporary("no access to the spreadsheet: share it with " + client.Email() + " as Editor")
		}
		return forms.Temporary(sheetsErrText(err))
	}
	sh, err := client.Resolve(ctx, ref)
	if err != nil {
		return fail(err)
	}
	a1 := sheets.QuoteSheet(sh.Title)
	rows, err := client.Values(ctx, ref.SpreadsheetID, a1+"!1:1")
	if err != nil {
		return fail(err)
	}
	var cur []string
	if len(rows) > 0 {
		cur = rows[0]
	}
	header, row, changed := sheetRow(cur, m)
	if changed {
		if err := client.Update(ctx, ref.SpreadsheetID, a1+"!1:1", [][]string{header}); err != nil {
			return fail(err)
		}
	}
	if err := client.Append(ctx, ref.SpreadsheetID, a1, [][]string{row}); err != nil {
		return fail(err)
	}
	return nil
}
