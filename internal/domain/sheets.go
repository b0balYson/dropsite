package domain

import "time"

// Google Sheets error codes (change google-sheets-sync).
const (
	// CodeSheetsNotConfigured: the administrator has not set the service
	// account key.
	CodeSheetsNotConfigured = "sheets_not_configured"
	// CodeSheetsInvalidURL rejects a link that is not a spreadsheet link.
	CodeSheetsInvalidURL = "sheets_invalid_url"
	// CodeSheetsAccessDenied: the service account cannot open or write the
	// spreadsheet or sheet.
	CodeSheetsAccessDenied = "sheets_access_denied"
	// CodeSheetsInvalid covers an invalid interval, a moderated collection
	// or an invalid service account key.
	CodeSheetsInvalid = "sheets_invalid"
	// CodeSheetsUnavailable: Google did not answer or refused the call.
	CodeSheetsUnavailable = "sheets_unavailable"
)

// SheetSyncStatus is the outcome of the last synchronization.
type SheetSyncStatus string

const (
	SheetSyncNone   SheetSyncStatus = ""
	SheetSyncOK     SheetSyncStatus = "ok"
	SheetSyncFailed SheetSyncStatus = "failed"
)

// Sync triggers.
const (
	SheetTriggerManual   = "manual"
	SheetTriggerSchedule = "schedule"
)

// SheetRowError is one problem found in a sheet row. Row is the row number
// in the sheet (1 is the header row); Field may be empty.
type SheetRowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// SheetLink ties a site collection to a sheet of a Google spreadsheet; the
// sheet is the source of truth for the collection's documents.
type SheetLink struct {
	SiteID        string
	Collection    string
	SpreadsheetID string
	// SheetGID is nil for the first sheet.
	SheetGID   *int64
	SheetTitle string
	// IntervalMinutes is 0 for manual synchronization only.
	IntervalMinutes int
	NextSyncAt      *time.Time
	LastSyncAt      *time.Time
	LastStatus      SheetSyncStatus
	LastTrigger     string
	LastRows        int
	LastMessage     string
	LastErrors      []SheetRowError
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SheetSyncResult is what one synchronization recorded.
type SheetSyncResult struct {
	At         time.Time
	Status     SheetSyncStatus
	Trigger    string
	Rows       int
	Message    string
	Errors     []SheetRowError
	SheetTitle string
}
