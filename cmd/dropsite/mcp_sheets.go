package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Google Sheets tools of the local server, through the API.

var _ mcp.SheetsBackend = localBackend{}

func sheetError(err error) error { return toolErrorFor(err, mcp.NotFoundSheet) }

func fromSheetLink(l client.SheetLink) mcp.SheetLink {
	out := mcp.SheetLink{
		Collection: l.Collection, URL: l.URL, SheetTitle: l.SheetTitle, IntervalMinutes: l.IntervalMinutes,
		NextSyncAt: stampTime(l.NextSyncAt), ServiceAccountEmail: deref(l.ServiceAccountEmail),
		Errors: []mcp.SheetRowError{},
	}
	if s := l.LastSync; s != nil {
		out.LastSyncAt, out.LastStatus, out.LastTrigger = stampTime(&s.At), s.Status, s.Trigger
		out.LastRows, out.LastMessage = s.Rows, s.Message
		for _, e := range s.Errors {
			out.Errors = append(out.Errors, mcp.SheetRowError{Row: e.Row, Field: e.Field, Message: e.Message})
		}
	}
	return out
}

func (b localBackend) GetSheetsSetup(ctx context.Context) (mcp.SheetsSetup, error) {
	st, err := b.c.GetSheetsStatus(ctx)
	if err != nil {
		return mcp.SheetsSetup{}, toolErrorFor(err, mcp.NotFoundSheet)
	}
	return mcp.SheetsSetup{Configured: st.Configured, ServiceAccountEmail: deref(st.ServiceAccountEmail)}, nil
}

func (b localBackend) GetSheetLink(ctx context.Context, ref, collection string) (mcp.SheetLink, error) {
	l, err := b.c.GetSheetLink(ctx, ref, collection)
	if err != nil {
		return mcp.SheetLink{}, sheetError(err)
	}
	return fromSheetLink(*l), nil
}

func (b localBackend) LinkSheet(ctx context.Context, ref, collection, url string, interval int) (mcp.SheetLink, error) {
	l, err := b.c.PutSheetLink(ctx, ref, collection, client.SheetLinkRequest{URL: url, IntervalMinutes: interval})
	if err != nil {
		return mcp.SheetLink{}, sheetError(err)
	}
	return fromSheetLink(*l), nil
}

func (b localBackend) SyncSheet(ctx context.Context, ref, collection string) (mcp.SheetLink, error) {
	l, err := b.c.SyncSheet(ctx, ref, collection)
	if err != nil {
		return mcp.SheetLink{}, sheetError(err)
	}
	return fromSheetLink(*l), nil
}

func (b localBackend) UnlinkSheet(ctx context.Context, ref, collection string) error {
	return sheetError(b.c.DeleteSheetLink(ctx, ref, collection))
}
