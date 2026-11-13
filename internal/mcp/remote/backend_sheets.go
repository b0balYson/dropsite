package remote

import (
	"context"
	"errors"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.SheetsBackend = backend{}

func (b backend) sheetError(err error) error {
	if err == nil {
		return nil
	}
	if e := domain.AsError(err); e.Kind == domain.KindNotFound {
		return errors.New(mcp.NotFoundSheet)
	}
	return b.toolError(err)
}

func toSheetLink(v service.SheetLinkView) mcp.SheetLink {
	l := v.Link
	out := mcp.SheetLink{
		Collection: l.Collection, URL: v.URL, SheetTitle: l.SheetTitle, IntervalMinutes: l.IntervalMinutes,
		LastSyncAt: stamp(l.LastSyncAt), LastStatus: string(l.LastStatus), LastTrigger: l.LastTrigger,
		LastRows: l.LastRows, LastMessage: l.LastMessage, ServiceAccountEmail: v.ServiceAccountEmail,
		Errors: make([]mcp.SheetRowError, 0, len(l.LastErrors)),
	}
	if l.IntervalMinutes > 0 {
		out.NextSyncAt = stamp(l.NextSyncAt)
	}
	for _, e := range l.LastErrors {
		out.Errors = append(out.Errors, mcp.SheetRowError{Row: e.Row, Field: e.Field, Message: e.Message})
	}
	return out
}

func (b backend) GetSheetsSetup(ctx context.Context) (mcp.SheetsSetup, error) {
	st, err := b.svc.GetSheetsStatus(ctx)
	if err != nil {
		return mcp.SheetsSetup{}, b.toolError(err)
	}
	return mcp.SheetsSetup{Configured: st.Configured, ServiceAccountEmail: st.Email}, nil
}

func (b backend) GetSheetLink(ctx context.Context, ref, collection string) (mcp.SheetLink, error) {
	v, err := b.svc.GetSheetLink(ctx, b.actor, ref, collection)
	if err != nil {
		return mcp.SheetLink{}, b.sheetError(err)
	}
	return toSheetLink(v), nil
}

func (b backend) LinkSheet(ctx context.Context, ref, collection, url string, interval int) (mcp.SheetLink, error) {
	v, _, err := b.svc.PutSheetLink(ctx, b.actor, ref, collection, service.SheetLinkInput{URL: url, IntervalMinutes: interval})
	if err != nil {
		return mcp.SheetLink{}, b.sheetError(err)
	}
	return toSheetLink(v), nil
}

func (b backend) SyncSheet(ctx context.Context, ref, collection string) (mcp.SheetLink, error) {
	v, err := b.svc.SyncSheet(ctx, b.actor, ref, collection)
	if err != nil {
		return mcp.SheetLink{}, b.sheetError(err)
	}
	return toSheetLink(v), nil
}

func (b backend) UnlinkSheet(ctx context.Context, ref, collection string) error {
	return b.sheetError(b.svc.DeleteSheetLink(ctx, b.actor, ref, collection))
}
