package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/sheets/sheetstest"
	"github.com/b0balYson/dropsite/internal/testenv"
)

const apiSheetDoc = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"

func TestSheetsAPI(t *testing.T) {
	fake := &sheetstest.Fake{}
	h := newHarness(t, testenv.WithSheetsTransport(fake))
	_, owner := h.user("owner@pp.test")
	_, other := h.user("other@pp.test")
	site := h.createSite(owner, "Cafe", "")
	base := "/api/v1/sites/" + site.ID + "/collections"
	sheetURL := "https://docs.google.com/spreadsheets/d/" + apiSheetDoc + "/edit#gid=3"

	rec := h.do(http.MethodGet, "/api/v1/sheets", owner, nil)
	wantStatus(t, rec, http.StatusOK)
	if st := decodeAs[sheetsStatusDTO](t, rec); st.Configured || st.ServiceAccountEmail != nil {
		t.Fatalf("status %+v", st)
	}
	wantStatus(t, h.do(http.MethodPost, base, owner, map[string]any{"name": "prices", "mode": "public"}), http.StatusCreated)
	wantError(t, h.do(http.MethodPut, base+"/prices/sheet", owner, map[string]any{"url": sheetURL}),
		http.StatusBadRequest, "sheets_not_configured")

	if _, err := h.env.Svc.SetSheetsServiceAccount(context.Background(), h.env.Admin, string(sheetstest.Key())); err != nil {
		t.Fatal(err)
	}
	rec = h.do(http.MethodGet, "/api/v1/sheets", owner, nil)
	if st := decodeAs[sheetsStatusDTO](t, rec); !st.Configured || st.ServiceAccountEmail == nil || *st.ServiceAccountEmail != sheetstest.Email {
		t.Fatalf("status %+v", st)
	}
	if strings.Contains(rec.Body.String(), "PRIVATE") {
		t.Fatal("key leaked")
	}

	fake.Add(apiSheetDoc, &sheetstest.Tab{ID: 3, Title: "Menu", Rows: [][]string{{"name", "price"}, {"Tea", "90"}}})
	wantError(t, h.do(http.MethodPut, base+"/prices/sheet", owner, map[string]any{"url": "https://example.com"}),
		http.StatusBadRequest, "sheets_invalid_url")
	wantError(t, h.do(http.MethodPut, base+"/prices/sheet", owner, map[string]any{"url": sheetURL, "interval_minutes": 1}),
		http.StatusBadRequest, "sheets_invalid")
	wantError(t, h.do(http.MethodPut, base+"/prices/sheet", other, map[string]any{"url": sheetURL}),
		http.StatusNotFound, "not_found")

	rec = h.do(http.MethodPut, base+"/prices/sheet", owner, map[string]any{"url": sheetURL, "interval_minutes": 30})
	wantStatus(t, rec, http.StatusCreated)
	l := decodeAs[sheetLinkDTO](t, rec)
	if l.URL != sheetURL || l.SheetTitle != "Menu" || l.IntervalMinutes != 30 || l.NextSyncAt == nil ||
		l.LastSync == nil || l.LastSync.Status != "ok" || l.LastSync.Rows != 1 || l.LastSync.Errors == nil {
		t.Fatalf("link %+v", l)
	}
	rec = h.do(http.MethodPut, base+"/prices/sheet", owner, map[string]any{"url": sheetURL})
	wantStatus(t, rec, http.StatusOK)
	if l := decodeAs[sheetLinkDTO](t, rec); l.NextSyncAt != nil || l.IntervalMinutes != 0 {
		t.Fatalf("manual link %+v", l)
	}

	fake.SetRows(apiSheetDoc, 3, [][]string{{"name", "name"}})
	rec = h.do(http.MethodPost, base+"/prices/sheet/sync", owner, nil)
	wantStatus(t, rec, http.StatusOK)
	if l := decodeAs[sheetLinkDTO](t, rec); l.LastSync.Status != "failed" || len(l.LastSync.Errors) != 1 || l.LastSync.Errors[0].Row != 1 {
		t.Fatalf("failed sync %+v", l.LastSync)
	}
	wantStatus(t, h.do(http.MethodGet, base+"/prices/sheet", owner, nil), http.StatusOK)
	wantError(t, h.do(http.MethodGet, base+"/prices/sheet", other, nil), http.StatusNotFound, "not_found")
	wantStatus(t, h.do(http.MethodDelete, base+"/prices/sheet", owner, nil), http.StatusNoContent)
	wantError(t, h.do(http.MethodGet, base+"/prices/sheet", owner, nil), http.StatusNotFound, "not_found")
	wantError(t, h.do(http.MethodPost, base+"/prices/sheet/sync", owner, nil), http.StatusNotFound, "not_found")
}
