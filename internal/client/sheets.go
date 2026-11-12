package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// SheetsStatus tells whether Google Sheets is set up on the server.
type SheetsStatus struct {
	Configured          bool    `json:"configured"`
	ServiceAccountEmail *string `json:"service_account_email"`
}

// SheetRowError is a problem in one row of a linked sheet.
type SheetRowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// SheetSync is the outcome of the last synchronization.
type SheetSync struct {
	At      time.Time       `json:"at"`
	Status  string          `json:"status"`
	Trigger string          `json:"trigger"`
	Rows    int             `json:"rows"`
	Message string          `json:"message"`
	Errors  []SheetRowError `json:"errors"`
}

// SheetLink is a collection linked to a Google Sheet.
type SheetLink struct {
	Collection          string     `json:"collection"`
	URL                 string     `json:"url"`
	SpreadsheetID       string     `json:"spreadsheet_id"`
	SheetGID            *int64     `json:"sheet_gid"`
	SheetTitle          string     `json:"sheet_title"`
	IntervalMinutes     int        `json:"interval_minutes"`
	NextSyncAt          *time.Time `json:"next_sync_at"`
	LastSync            *SheetSync `json:"last_sync"`
	ServiceAccountEmail *string    `json:"service_account_email"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// SheetLinkRequest is the body of PUT .../collections/{name}/sheet.
type SheetLinkRequest struct {
	URL             string `json:"url"`
	IntervalMinutes int    `json:"interval_minutes"`
}

func sheetPath(ref, name string) string {
	return "/sites/" + url.PathEscape(ref) + "/collections/" + url.PathEscape(name) + "/sheet"
}

// GetSheetsStatus reports the Google Sheets setup of the server.
func (c *Client) GetSheetsStatus(ctx context.Context) (*SheetsStatus, error) {
	var out SheetsStatus
	if err := c.doJSON(ctx, http.MethodGet, "/sheets", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSheetLink returns the sheet link of a collection.
func (c *Client) GetSheetLink(ctx context.Context, ref, name string) (*SheetLink, error) {
	var out SheetLink
	if err := c.doJSON(ctx, http.MethodGet, sheetPath(ref, name), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutSheetLink links a collection to a sheet and synchronizes it.
func (c *Client) PutSheetLink(ctx context.Context, ref, name string, req SheetLinkRequest) (*SheetLink, error) {
	var out SheetLink
	if err := c.doJSON(ctx, http.MethodPut, sheetPath(ref, name), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SyncSheet synchronizes a linked collection now.
func (c *Client) SyncSheet(ctx context.Context, ref, name string) (*SheetLink, error) {
	var out SheetLink
	if err := c.doJSON(ctx, http.MethodPost, sheetPath(ref, name)+"/sync", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSheetLink unlinks a collection; its documents stay.
func (c *Client) DeleteSheetLink(ctx context.Context, ref, name string) error {
	return c.doJSON(ctx, http.MethodDelete, sheetPath(ref, name), nil, nil, nil)
}
