package mcp

import (
	"context"
	"fmt"
	"strings"
)

// SheetsBackend links site collections to Google Sheets. It never handles
// the service account key: the administrator sets it in the web interface.
type SheetsBackend interface {
	GetSheetsSetup(ctx context.Context) (SheetsSetup, error)
	GetSheetLink(ctx context.Context, ref, collection string) (SheetLink, error)
	LinkSheet(ctx context.Context, ref, collection, url string, intervalMinutes int) (SheetLink, error)
	SyncSheet(ctx context.Context, ref, collection string) (SheetLink, error)
	UnlinkSheet(ctx context.Context, ref, collection string) error
}

// SheetsSetup is the output of get_sheets_setup.
type SheetsSetup struct {
	Configured          bool   `json:"configured" jsonschema:"whether Google Sheets is connected on this dropsite server"`
	ServiceAccountEmail string `json:"service_account_email,omitempty" jsonschema:"the address the user shares the spreadsheet with"`
	NextStep            string `json:"next_step" jsonschema:"what to tell the user and do next"`
}

// SheetRowError is a problem in one row of the sheet.
type SheetRowError struct {
	Row     int    `json:"row" jsonschema:"row number in the sheet; 1 is the header row, 0 the whole sheet"`
	Field   string `json:"field,omitempty" jsonschema:"column (field) name"`
	Message string `json:"message"`
}

// SheetLink is a collection linked to a sheet with its last synchronization.
type SheetLink struct {
	Collection          string          `json:"collection"`
	URL                 string          `json:"url" jsonschema:"link to the spreadsheet and sheet"`
	SheetTitle          string          `json:"sheet_title,omitempty" jsonschema:"name of the sheet (tab)"`
	IntervalMinutes     int             `json:"interval_minutes" jsonschema:"automatic synchronization period; 0 means only on request"`
	NextSyncAt          string          `json:"next_sync_at,omitempty" jsonschema:"UTC, RFC 3339"`
	LastSyncAt          string          `json:"last_sync_at,omitempty" jsonschema:"UTC, RFC 3339"`
	LastStatus          string          `json:"last_status,omitempty" jsonschema:"ok or failed"`
	LastTrigger         string          `json:"last_trigger,omitempty" jsonschema:"manual or schedule"`
	LastRows            int             `json:"last_rows" jsonschema:"documents written by the last successful synchronization"`
	LastMessage         string          `json:"last_message,omitempty"`
	Errors              []SheetRowError `json:"errors" jsonschema:"problems found in rows by the last synchronization (at most 50)"`
	ServiceAccountEmail string          `json:"service_account_email,omitempty" jsonschema:"the address the spreadsheet must be shared with"`
	Summary             string          `json:"summary" jsonschema:"the state in plain words, ready to tell the user"`
}

// NotFoundSheet is the not-found text of the sheet tools.
const NotFoundSheet = "not found: no such site, collection or sheet link among this user's sites; call " +
	"list_collections to see the collections, and link_sheet to link one"

// SheetsSetupNextStep tells the model what to say about the setup.
func SheetsSetupNextStep(s SheetsSetup) string {
	if !s.Configured {
		return "Google Sheets is not connected on this Dropsite server. Tell the user that the administrator of " +
			"the server must add a Google service account key in the settings of the web interface; there is " +
			"nothing to do in the chat. Never ask anyone for the key."
	}
	return "Ask the user to open the spreadsheet in Google Sheets, press Share and add " + s.ServiceAccountEmail +
		" (Editor for form submissions, Viewer is enough for site data), then send the link to the sheet " +
		"(tab) from the browser address bar. Then use add_channel with kind sheets for submissions, or " +
		"link_sheet to show the sheet's rows on the site."
}

// SheetLinkSummary describes a link in plain words.
func SheetLinkSummary(l SheetLink) string {
	var b strings.Builder
	sheet := l.URL
	if l.SheetTitle != "" {
		sheet = "sheet \"" + l.SheetTitle + "\" (" + l.URL + ")"
	}
	fmt.Fprintf(&b, "Collection %s takes its documents from %s; edits made to it in the chat are overwritten by "+
		"the next synchronization. ", l.Collection, sheet)
	if l.IntervalMinutes > 0 {
		fmt.Fprintf(&b, "It synchronizes every %d minutes", l.IntervalMinutes)
		if l.NextSyncAt != "" {
			b.WriteString(" (next at " + l.NextSyncAt + ")")
		}
		b.WriteString(". ")
	} else {
		b.WriteString("It synchronizes only on request (sync_sheet). ")
	}
	switch l.LastStatus {
	case "ok":
		fmt.Fprintf(&b, "Last synchronization at %s: %d row(s) are on the site.", l.LastSyncAt, l.LastRows)
	case "failed":
		fmt.Fprintf(&b, "Last synchronization at %s failed, the site still shows the previous data: %s", l.LastSyncAt, l.LastMessage)
		for i, e := range l.Errors {
			if i == 5 {
				fmt.Fprintf(&b, " … and %d more.", len(l.Errors)-i)
				break
			}
			b.WriteString(" Row " + fmt.Sprint(e.Row))
			if e.Field != "" {
				b.WriteString(", column " + e.Field)
			}
			b.WriteString(": " + e.Message + ".")
		}
	default:
		b.WriteString("It has not been synchronized yet.")
	}
	return b.String()
}
