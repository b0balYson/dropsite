package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/sheets"
	"github.com/b0balYson/dropsite/internal/store"
)

// Periodic synchronization of collections linked to sheets.
const (
	MinSheetInterval = 5
	MaxSheetInterval = 1440
	// SheetSyncCheckInterval is how often control looks for due links.
	SheetSyncCheckInterval = time.Minute
	sheetSyncBatch         = 20
)

// SheetLinkView is a link as shown to the collection's owner.
type SheetLinkView struct {
	Link domain.SheetLink
	// URL is the canonical spreadsheet link with the sheet.
	URL string
	// ServiceAccountEmail is "" when Google Sheets is not configured.
	ServiceAccountEmail string
}

// SheetLinkInput links a collection to a sheet.
type SheetLinkInput struct {
	URL             string
	IntervalMinutes int
}

func sheetLinkURL(l domain.SheetLink) string {
	return sheets.Ref{SpreadsheetID: l.SpreadsheetID, GID: l.SheetGID}.URL()
}

func (s *Service) sheetLinkView(ctx context.Context, l domain.SheetLink) SheetLinkView {
	v := SheetLinkView{Link: l, URL: sheetLinkURL(l)}
	if st, err := s.GetSheetsStatus(ctx); err == nil {
		v.ServiceAccountEmail = st.Email
	}
	return v
}

func sheetInvalid(field, msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeSheetsInvalid, msg).WithParams(map[string]any{"field": field})
}

func (s *Service) allowSheetOp(siteID string) error {
	if ok, retry := s.sheetsSiteLimit.Allow(siteID); !ok {
		return domain.ErrRateLimited.WithRetryAfter(retry)
	}
	return nil
}

// GetSheetLink returns the link of the actor's collection.
func (s *Service) GetSheetLink(ctx context.Context, a Actor, ref, name string) (SheetLinkView, error) {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return SheetLinkView{}, err
	}
	l, err := s.repo.GetSheetLink(ctx, c.SiteID, c.Name)
	if err != nil {
		return SheetLinkView{}, err
	}
	return s.sheetLinkView(ctx, l), nil
}

// PutSheetLink links a public collection to a sheet (or changes the link)
// and synchronizes it at once. It reports whether the link is new. A
// synchronization that finds bad rows still keeps the link; its result is
// in the view.
func (s *Service) PutSheetLink(ctx context.Context, a Actor, ref, name string, in SheetLinkInput) (SheetLinkView, bool, error) {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return SheetLinkView{}, false, err
	}
	if c.Mode != domain.CollectionPublic {
		return SheetLinkView{}, false, sheetInvalid("collection", "only a public collection can be linked to a sheet: "+
			"visitors propose the documents of a moderated one")
	}
	if in.IntervalMinutes != 0 && (in.IntervalMinutes < MinSheetInterval || in.IntervalMinutes > MaxSheetInterval) {
		return SheetLinkView{}, false, sheetInvalid("interval_minutes",
			fmt.Sprintf("interval_minutes must be 0 (manual only) or from %d to %d", MinSheetInterval, MaxSheetInterval))
	}
	client, err := s.sheetsAPI(ctx)
	if err != nil {
		return SheetLinkView{}, false, err
	}
	sref, err := sheets.ParseURL(in.URL)
	if err != nil {
		return SheetLinkView{}, false, s.sheetsCallErr(err, client.Email(), false)
	}
	if err := s.allowSheetOp(c.SiteID); err != nil {
		return SheetLinkView{}, false, err
	}
	cctx, cancel := context.WithTimeout(ctx, SheetCallTimeout)
	sh, err := client.Resolve(cctx, sref)
	cancel()
	if err != nil {
		return SheetLinkView{}, false, s.sheetsCallErr(err, client.Email(), false)
	}
	now := s.now()
	gid := sh.ID
	l := domain.SheetLink{SiteID: c.SiteID, Collection: c.Name, SpreadsheetID: sref.SpreadsheetID, SheetGID: &gid,
		SheetTitle: sh.Title, IntervalMinutes: in.IntervalMinutes, CreatedAt: now, UpdatedAt: now}
	if in.IntervalMinutes > 0 {
		next := now.Add(time.Duration(in.IntervalMinutes) * time.Minute)
		l.NextSyncAt = &next
	}
	created, err := s.repo.PutSheetLink(ctx, l)
	if err != nil {
		return SheetLinkView{}, false, err
	}
	s.audit(ctx, a, "collection.sheet.link", siteTarget(c.SiteID), map[string]any{
		"name": c.Name, "spreadsheet_id": l.SpreadsheetID, "sheet_gid": gid, "interval_minutes": l.IntervalMinutes,
	})
	s.syncLinked(ctx, a, c, l, domain.SheetTriggerManual)
	l, err = s.repo.GetSheetLink(ctx, c.SiteID, c.Name)
	if err != nil {
		return SheetLinkView{}, false, err
	}
	return s.sheetLinkView(ctx, l), created, nil
}

// DeleteSheetLink unlinks a collection; its documents stay.
func (s *Service) DeleteSheetLink(ctx context.Context, a Actor, ref, name string) error {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteSheetLink(ctx, c.SiteID, c.Name); err != nil {
		return err
	}
	s.audit(ctx, a, "collection.sheet.unlink", siteTarget(c.SiteID), map[string]any{"name": c.Name})
	return nil
}

// SyncSheet replaces the documents of a linked collection with the rows of
// its sheet now. Bad rows are reported in the result and change nothing.
func (s *Service) SyncSheet(ctx context.Context, a Actor, ref, name string) (SheetLinkView, error) {
	_, c, err := s.ownedCollection(ctx, a, ref, name)
	if err != nil {
		return SheetLinkView{}, err
	}
	l, err := s.repo.GetSheetLink(ctx, c.SiteID, c.Name)
	if err != nil {
		return SheetLinkView{}, err
	}
	if err := s.allowSheetOp(c.SiteID); err != nil {
		return SheetLinkView{}, err
	}
	s.syncLinked(ctx, a, c, l, domain.SheetTriggerManual)
	l, err = s.repo.GetSheetLink(ctx, c.SiteID, c.Name)
	if err != nil {
		return SheetLinkView{}, err
	}
	return s.sheetLinkView(ctx, l), nil
}

// syncLinked runs one synchronization and records its outcome.
func (s *Service) syncLinked(ctx context.Context, a Actor, c domain.Collection, l domain.SheetLink, trigger string) domain.SheetSyncResult {
	ctx = context.WithoutCancel(ctx)
	res := s.syncSheet(ctx, c, l)
	res.At, res.Trigger = s.now(), trigger
	if err := s.repo.RecordSheetSync(ctx, c.SiteID, c.Name, res); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.log.Error("record sheet sync", "site", c.SiteID, "collection", c.Name, "err", err)
	}
	s.audit(ctx, a, "collection.sheet.sync", siteTarget(c.SiteID), map[string]any{
		"name": c.Name, "trigger": trigger, "status": string(res.Status), "rows": res.Rows,
	})
	return res
}

func failedSync(msg string) domain.SheetSyncResult {
	return domain.SheetSyncResult{Status: domain.SheetSyncFailed, Message: msg, Errors: []domain.SheetRowError{}}
}

// syncSheet reads the sheet and replaces the documents when every row is
// valid.
func (s *Service) syncSheet(ctx context.Context, c domain.Collection, l domain.SheetLink) domain.SheetSyncResult {
	if c.Mode != domain.CollectionPublic {
		return failedSync("the collection is moderated now; only a public collection can be synchronized from a sheet")
	}
	client, err := s.sheetsAPI(ctx)
	if err != nil {
		return failedSync(domain.AsError(err).Message)
	}
	cctx, cancel := context.WithTimeout(ctx, SheetCallTimeout)
	defer cancel()
	sh, err := client.Resolve(cctx, sheets.Ref{SpreadsheetID: l.SpreadsheetID, GID: l.SheetGID})
	if err != nil {
		return failedSync(domain.AsError(s.sheetsCallErr(err, client.Email(), false)).Message)
	}
	rows, err := client.Values(cctx, l.SpreadsheetID, sheets.QuoteSheet(sh.Title))
	if err != nil {
		res := failedSync(domain.AsError(s.sheetsCallErr(err, client.Email(), false)).Message)
		res.SheetTitle = sh.Title
		return res
	}
	rs, err := s.schemas.get(c)
	if err != nil {
		res := failedSync("the collection schema cannot be used: " + domain.AsError(err).Message)
		res.SheetTitle = sh.Title
		return res
	}
	built := sheetDocuments(c, rs, rows)
	if built.failed > 0 {
		res := failedSync(fmt.Sprintf("%d row(s) have problems; nothing was changed. Fix them in the sheet and "+
			"synchronize again", built.failed))
		res.Errors, res.SheetTitle = built.errs, sh.Title
		return res
	}
	if err := s.repo.SaveDocuments(ctx, store.DocumentsWrite{SiteID: c.SiteID, Collection: c.Name, Docs: built.docs,
		Replace: true, MaxDocs: MaxDocumentsPerCollection, Now: s.now()}); err != nil {
		de := domain.AsError(err)
		msg := de.Message
		if de.Kind == domain.KindInternal {
			s.log.Error("save synchronized documents", "site", c.SiteID, "collection", c.Name, "err", err)
			msg = "the documents could not be saved; try again later"
		}
		res := failedSync(msg)
		res.SheetTitle = sh.Title
		return res
	}
	return domain.SheetSyncResult{Status: domain.SheetSyncOK, Rows: len(built.docs), SheetTitle: sh.Title,
		Message: fmt.Sprintf("%d row(s) synchronized", len(built.docs)), Errors: []domain.SheetRowError{}}
}

// SyncDueSheets runs the periodic synchronizations that are due and
// returns how many ran.
func (s *Service) SyncDueSheets(ctx context.Context) (int, error) {
	links, err := s.repo.ClaimDueSheetLinks(ctx, s.now(), sheetSyncBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range links {
		if ctx.Err() != nil {
			break
		}
		c, err := s.repo.GetCollection(ctx, l.SiteID, l.Collection)
		if err != nil {
			continue // removed meanwhile
		}
		s.syncLinked(ctx, Actor{}, c, l, domain.SheetTriggerSchedule)
		n++
	}
	return n, nil
}

// RunSheetSync runs SyncDueSheets every interval until ctx is done.
func (s *Service) RunSheetSync(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.SyncDueSheets(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("sheet synchronization failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
