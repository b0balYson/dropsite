// Package sheets is a small Google Sheets client for a service account:
// spreadsheet links, the service account key, RS256-signed token requests
// and the four Sheets API v4 calls Dropsite needs. It talks only to
// oauth2.googleapis.com and sheets.googleapis.com over https.
package sheets

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalidURL rejects a link that is not a Google spreadsheet link.
var ErrInvalidURL = errors.New("not a Google Sheets link: open the spreadsheet in the browser and copy the address " +
	"(https://docs.google.com/spreadsheets/d/...)")

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,100}$`)

// Ref points to a spreadsheet and optionally one of its sheets (tabs).
// GID nil means the first sheet.
type Ref struct {
	SpreadsheetID string
	GID           *int64
}

// ParseURL reads a spreadsheet link such as
// https://docs.google.com/spreadsheets/d/<id>/edit#gid=123. The sheet ID
// (gid) may be in the fragment or the query.
func ParseURL(raw string) (Ref, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 {
		return Ref{}, ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Hostname(), "docs.google.com") || u.Port() != "" {
		return Ref{}, ErrInvalidURL
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	// Workspace links look like /a/<domain>/spreadsheets/d/<id>.
	if len(parts) >= 2 && parts[0] == "a" {
		parts = parts[2:]
	}
	if len(parts) < 3 || parts[0] != "spreadsheets" || parts[1] != "d" || !idPattern.MatchString(parts[2]) {
		return Ref{}, ErrInvalidURL
	}
	ref := Ref{SpreadsheetID: parts[2]}
	gid := u.Query().Get("gid")
	if frag, err := url.ParseQuery(u.Fragment); err == nil && frag.Get("gid") != "" {
		gid = frag.Get("gid")
	}
	if gid != "" {
		n, err := strconv.ParseInt(gid, 10, 64)
		if err != nil || n < 0 {
			return Ref{}, ErrInvalidURL
		}
		ref.GID = &n
	}
	return ref, nil
}

// URL is the canonical link of the spreadsheet and sheet.
func (r Ref) URL() string {
	u := "https://docs.google.com/spreadsheets/d/" + r.SpreadsheetID + "/edit"
	if r.GID != nil {
		u += "#gid=" + strconv.FormatInt(*r.GID, 10)
	}
	return u
}

// QuoteSheet returns a sheet title as an A1 range prefix: the title in
// single quotes, with every single quote inside doubled.
func QuoteSheet(title string) string {
	return "'" + strings.ReplaceAll(title, "'", "''") + "'"
}
