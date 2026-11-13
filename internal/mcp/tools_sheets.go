package mcp

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Google Sheets tools, registered with the collection tools; the backend
// must implement SheetsBackend. No tool takes or returns the service
// account key.

type sheetCollectionArgs struct {
	Site       string `json:"site" jsonschema:"site ID or slug"`
	Collection string `json:"collection" jsonschema:"collection name from list_collections"`
}

type linkSheetArgs struct {
	Site            string `json:"site" jsonschema:"site ID or slug"`
	Collection      string `json:"collection" jsonschema:"name of a public collection from list_collections"`
	URL             string `json:"url" jsonschema:"link to the sheet as copied from the browser, e.g. https://docs.google.com/spreadsheets/d/<id>/edit#gid=0"`
	IntervalMinutes int    `json:"interval_minutes,omitempty" jsonschema:"synchronize automatically every N minutes (5-1440); 0 or omitted: only when sync_sheet is called"`
	Confirm         string `json:"confirm" jsonschema:"the collection name typed again after the user agreed that the sheet replaces the collection's documents"`
}

type sheetUnlinked struct {
	Unlinked string `json:"unlinked" jsonschema:"collection that is no longer linked; its documents stay"`
}

// ErrSheetConfirm is returned by link_sheet when confirm does not repeat
// the collection name.
var ErrSheetConfirm = errors.New("nothing linked: tell the user that the rows of the sheet will replace the " +
	"current documents of the collection, ask to confirm, then call again with confirm set to the collection name")

var errNoSheets = errors.New("this server cannot connect Google Sheets; use the web interface of Dropsite")

func (t tools) registerSheets(s *sdk.Server, o Options) {
	if !o.Collections {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "get_sheets_setup", Annotations: readOnly("Google Sheets setup"),
		Description: "Check whether Google Sheets can be connected and get the address the user must share the " +
			"spreadsheet with. Call it first when the user wants form submissions in a Google Sheet or wants site " +
			"data (prices, menu, schedule, catalog) to come from a Google Sheet. Tell the user next_step in " +
			"plain words: open the spreadsheet, press Share, add the address, send the link. When configured is " +
			"false, only an administrator can add the Google key in the web interface: never ask for it in the chat."}, t.getSheetsSetup)
	sdk.AddTool(s, &sdk.Tool{Name: "get_sheet_link", Annotations: readOnly("Show sheet link"),
		Description: "Show which Google Sheet a collection is linked to, how often it synchronizes and the result " +
			"of the last synchronization with problems per row. Use it when the user asks why the site does " +
			"not show a change made in the sheet."}, t.getSheetLink)
	sdk.AddTool(s, &sdk.Tool{Name: "link_sheet", Annotations: writes("Link Google Sheet", true, true),
		Description: "Make a Google Sheet the source of a public collection, so the user edits prices, a menu or a " +
			"schedule in the sheet and the site follows. Row 1 of the sheet holds the field names the pages read; " +
			"every other row becomes a document (a column named id sets stable IDs); values are converted to the " +
			"types of the collection schema. Linking synchronizes at once and REPLACES the collection's current " +
			"documents, and later edits made in the chat are overwritten: only after the user agreed, pass the " +
			"collection name again in confirm. The spreadsheet must be shared with the address from " +
			"get_sheets_setup. Tell the user the summary, including any row problems."}, t.linkSheet)
	sdk.AddTool(s, &sdk.Tool{Name: "sync_sheet", Annotations: writes("Sync Google Sheet", false, true),
		Description: "Copy the rows of the linked Google Sheet to the collection now (the user changed the sheet " +
			"and wants the site updated). If some rows are wrong nothing changes and the result lists them by row " +
			"number: tell the user which rows and columns to fix in the sheet."}, t.syncSheet)
	sdk.AddTool(s, &sdk.Tool{Name: "unlink_sheet", Annotations: writes("Unlink Google Sheet", false, true),
		Description: "Stop taking a collection's documents from its Google Sheet, when the user wants to edit them " +
			"in the chat again. The documents stay as they are."}, t.unlinkSheet)
}

func (t tools) sheets(ctx context.Context, req *sdk.CallToolRequest) (SheetsBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	s, ok := b.(SheetsBackend)
	if !ok {
		return nil, errNoSheets
	}
	return s, nil
}

func withSheetSummary(l SheetLink) SheetLink {
	if l.Errors == nil {
		l.Errors = []SheetRowError{}
	}
	l.Summary = SheetLinkSummary(l)
	return l
}

func (t tools) getSheetsSetup(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, SheetsSetup, error) {
	b, err := t.sheets(ctx, req)
	if err != nil {
		return nil, SheetsSetup{}, err
	}
	s, err := b.GetSheetsSetup(ctx)
	if err != nil {
		return nil, SheetsSetup{}, err
	}
	s.NextStep = SheetsSetupNextStep(s)
	return nil, s, nil
}

func (t tools) getSheetLink(ctx context.Context, req *sdk.CallToolRequest, in sheetCollectionArgs) (*sdk.CallToolResult, SheetLink, error) {
	b, err := t.sheets(ctx, req)
	if err != nil {
		return nil, SheetLink{}, err
	}
	l, err := b.GetSheetLink(ctx, in.Site, normName(in.Collection))
	if err != nil {
		return nil, SheetLink{}, err
	}
	return nil, withSheetSummary(l), nil
}

func (t tools) linkSheet(ctx context.Context, req *sdk.CallToolRequest, in linkSheetArgs) (*sdk.CallToolResult, SheetLink, error) {
	b, err := t.sheets(ctx, req)
	if err != nil {
		return nil, SheetLink{}, err
	}
	name := normName(in.Collection)
	if name == "" || normName(in.Confirm) != name {
		return nil, SheetLink{}, ErrSheetConfirm
	}
	l, err := b.LinkSheet(ctx, in.Site, name, strings.TrimSpace(in.URL), in.IntervalMinutes)
	if err != nil {
		return nil, SheetLink{}, err
	}
	return nil, withSheetSummary(l), nil
}

func (t tools) syncSheet(ctx context.Context, req *sdk.CallToolRequest, in sheetCollectionArgs) (*sdk.CallToolResult, SheetLink, error) {
	b, err := t.sheets(ctx, req)
	if err != nil {
		return nil, SheetLink{}, err
	}
	l, err := b.SyncSheet(ctx, in.Site, normName(in.Collection))
	if err != nil {
		return nil, SheetLink{}, err
	}
	return nil, withSheetSummary(l), nil
}

func (t tools) unlinkSheet(ctx context.Context, req *sdk.CallToolRequest, in sheetCollectionArgs) (*sdk.CallToolResult, sheetUnlinked, error) {
	b, err := t.sheets(ctx, req)
	if err != nil {
		return nil, sheetUnlinked{}, err
	}
	name := normName(in.Collection)
	if err := b.UnlinkSheet(ctx, in.Site, name); err != nil {
		return nil, sheetUnlinked{}, err
	}
	return nil, sheetUnlinked{Unlinked: name}, nil
}
