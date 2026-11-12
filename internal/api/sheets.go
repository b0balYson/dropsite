package api

import (
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Google Sheets endpoints (change google-sheets-sync). The service account
// key is never accepted or returned here: it is set in the web interface.
func (a *api) sheetsRoutes() []route {
	return []route{
		{Method: http.MethodGet, Path: "/sheets", handle: a.getSheetsStatus},
		{Method: http.MethodGet, Path: "/sites/{ref}/collections/{name}/sheet", handle: a.getSheetLink},
		{Method: http.MethodPut, Path: "/sites/{ref}/collections/{name}/sheet", handle: a.putSheetLink},
		{Method: http.MethodDelete, Path: "/sites/{ref}/collections/{name}/sheet", handle: a.deleteSheetLink},
		{Method: http.MethodPost, Path: "/sites/{ref}/collections/{name}/sheet/sync", handle: a.syncSheet},
	}
}

type sheetsStatusDTO struct {
	Configured          bool    `json:"configured"`
	ServiceAccountEmail *string `json:"service_account_email"`
}

func (a *api) getSheetsStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := a.svc.GetSheetsStatus(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sheetsStatusDTO{Configured: st.Configured, ServiceAccountEmail: optional(st.Email)})
	return nil
}

type sheetSyncDTO struct {
	At      time.Time              `json:"at"`
	Status  string                 `json:"status"`
	Trigger string                 `json:"trigger"`
	Rows    int                    `json:"rows"`
	Message string                 `json:"message"`
	Errors  []domain.SheetRowError `json:"errors"`
}

type sheetLinkDTO struct {
	Collection          string        `json:"collection"`
	URL                 string        `json:"url"`
	SpreadsheetID       string        `json:"spreadsheet_id"`
	SheetGID            *int64        `json:"sheet_gid"`
	SheetTitle          string        `json:"sheet_title"`
	IntervalMinutes     int           `json:"interval_minutes"`
	NextSyncAt          *time.Time    `json:"next_sync_at"`
	LastSync            *sheetSyncDTO `json:"last_sync"`
	ServiceAccountEmail *string       `json:"service_account_email"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

func toSheetLink(v service.SheetLinkView) sheetLinkDTO {
	l := v.Link
	out := sheetLinkDTO{
		Collection: l.Collection, URL: v.URL, SpreadsheetID: l.SpreadsheetID, SheetGID: l.SheetGID,
		SheetTitle: l.SheetTitle, IntervalMinutes: l.IntervalMinutes, NextSyncAt: l.NextSyncAt,
		ServiceAccountEmail: optional(v.ServiceAccountEmail), CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
	if l.IntervalMinutes == 0 {
		out.NextSyncAt = nil
	}
	if l.LastSyncAt != nil {
		errs := l.LastErrors
		if errs == nil {
			errs = []domain.SheetRowError{}
		}
		out.LastSync = &sheetSyncDTO{At: *l.LastSyncAt, Status: string(l.LastStatus), Trigger: l.LastTrigger,
			Rows: l.LastRows, Message: l.LastMessage, Errors: errs}
	}
	return out
}

func (a *api) getSheetLink(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.GetSheetLink(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSheetLink(v))
	return nil
}

type sheetLinkRequest struct {
	URL             string `json:"url"`
	IntervalMinutes int    `json:"interval_minutes"`
}

func (a *api) putSheetLink(w http.ResponseWriter, r *http.Request) error {
	var req sheetLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	v, created, err := a.svc.PutSheetLink(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"),
		service.SheetLinkInput{URL: req.URL, IntervalMinutes: req.IntervalMinutes})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, toSheetLink(v))
	return nil
}

func (a *api) deleteSheetLink(w http.ResponseWriter, r *http.Request) error {
	if err := a.svc.DeleteSheetLink(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name")); err != nil {
		return err
	}
	noContent(w)
	return nil
}

func (a *api) syncSheet(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.SyncSheet(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.PathValue("name"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSheetLink(v))
	return nil
}
