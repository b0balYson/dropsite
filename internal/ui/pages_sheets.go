package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// Google Sheets screens: the administrator's service account key (the only
// place it is entered) and the sheet block of a collection page.

// sheetIntervals are the choices of the synchronization period (minutes).
var sheetIntervals = []int{0, 5, 15, 30, 60, 360, 1440}

// sheetBlock is the "Google Sheet" block of a collection page.
type sheetBlock struct {
	Status    service.SheetsStatus
	Link      *service.SheetLinkView
	Public    bool
	Intervals []int
	Interval  int
}

// sheetBlockFor loads the block; failures only hide the link state.
func (s *server) sheetBlockFor(r *http.Request, rc *reqCtx, siteID string, c service.CollectionView) *sheetBlock {
	b := &sheetBlock{Public: c.Mode == domain.CollectionPublic, Intervals: sheetIntervals}
	if st, err := s.svc.GetSheetsStatus(r.Context()); err == nil {
		b.Status = st
	}
	v, err := s.svc.GetSheetLink(r.Context(), rc.actor, siteID, c.Name)
	switch {
	case err == nil:
		b.Link, b.Interval = &v, v.Link.IntervalMinutes
	case !errors.Is(err, domain.ErrNotFound):
		s.log.Warn("load sheet link", "site", siteID, "collection", c.Name, "err", err)
	}
	return b
}

func sheetBack(r *http.Request) string {
	return collectionPagePath(r.PathValue("ref"), r.PathValue("name")) + "#sheet-h"
}

// sheetOutcome flashes the result of a synchronization.
func (s *server) sheetOutcome(w http.ResponseWriter, r *http.Request, rc *reqCtx, v service.SheetLinkView, okKey string) {
	if v.Link.LastStatus == domain.SheetSyncFailed {
		s.setFlash(w, r, flashError, rc.loc.T("sheets.sync.failed", "message", v.Link.LastMessage))
	} else {
		s.setFlash(w, r, flashOK, rc.loc.T(okKey, "rows", v.Link.LastRows))
	}
	redirect(w, r, collectionPagePath(v.Link.SiteID, v.Link.Collection)+"#sheet-h")
}

func (s *server) linkSheet(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	interval, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("interval_minutes")))
	if err != nil {
		interval = 0
	}
	v, _, err := s.svc.PutSheetLink(r.Context(), rc.actor, r.PathValue("ref"), r.PathValue("name"),
		service.SheetLinkInput{URL: r.PostFormValue("url"), IntervalMinutes: interval})
	if err != nil {
		s.fail(w, r, rc, err, sheetBack(r))
		return
	}
	s.sheetOutcome(w, r, rc, v, "sheets.linked")
}

func (s *server) syncSheet(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	v, err := s.svc.SyncSheet(r.Context(), rc.actor, r.PathValue("ref"), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, rc, err, sheetBack(r))
		return
	}
	s.sheetOutcome(w, r, rc, v, "sheets.synced")
}

func (s *server) unlinkSheet(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.svc.DeleteSheetLink(r.Context(), rc.actor, r.PathValue("ref"), r.PathValue("name")); err != nil {
		s.fail(w, r, rc, err, sheetBack(r))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("sheets.unlinked"))
	redirect(w, r, sheetBack(r))
}

// saveSheetsKey stores or removes the service account key (admin).
func (s *server) saveSheetsKey(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	const back = "/admin/settings#sheets-h"
	if r.PostFormValue("clear") == "1" {
		if err := s.svc.ClearSheetsServiceAccount(r.Context(), rc.actor); err != nil {
			s.fail(w, r, rc, err, back)
			return
		}
		s.setFlash(w, r, flashOK, rc.loc.T("sheets.admin.cleared"))
		redirect(w, r, back)
		return
	}
	st, err := s.svc.SetSheetsServiceAccount(r.Context(), rc.actor, r.PostFormValue("service_account_json"))
	if err != nil {
		msg, _ := s.describe(rc, err)
		if de := domain.AsError(err); de.Code == domain.CodeSheetsInvalid || de.Code == domain.CodeSheetsUnavailable {
			msg += " " + de.Message
		}
		s.setFlash(w, r, flashError, msg)
		redirect(w, r, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("sheets.admin.saved", "email", st.Email))
	redirect(w, r, back)
}
