//go:build e2e

package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestGoogleSheetsNotConfigured checks the Google Sheets endpoints on the
// real stack without a service account: the state is reported, linking a
// collection and adding a sheets channel explain that the administrator
// must connect Google Sheets, and nothing calls Google.
func TestGoogleSheetsNotConfigured(t *testing.T) {
	s := up(t)
	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}
	ann := createUser(t, s, admin, "sheets@dropsite.localhost")

	st, body := ann.json(http.MethodGet, "/sheets", nil)
	must(t, st, http.StatusOK, body, "sheets status")
	if body["configured"] != false || body["service_account_email"] != nil {
		t.Fatalf("status: %v", body)
	}

	st, body = ann.json(http.MethodPost, "/sites", map[string]any{"title": "Prices"})
	must(t, st, http.StatusCreated, body, "create site")
	id := str(body, "id")
	st, body = ann.json(http.MethodPost, "/sites/"+id+"/collections", map[string]any{"name": "prices", "mode": "public"})
	must(t, st, http.StatusCreated, body, "create collection")

	sheet := "https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd/edit#gid=0"
	st, body = ann.json(http.MethodPut, "/sites/"+id+"/collections/prices/sheet", map[string]any{"url": sheet})
	if st != http.StatusBadRequest || errCode(body) != "sheets_not_configured" {
		t.Fatalf("link without a service account: %d %v", st, body)
	}
	st, body = ann.json(http.MethodPost, "/sites/"+id+"/forms/channels", map[string]any{"kind": "sheets", "target": sheet})
	if st != http.StatusBadRequest || errCode(body) != "sheets_not_configured" {
		t.Fatalf("sheets channel without a service account: %d %v", st, body)
	}
	st, body = ann.json(http.MethodGet, "/sites/"+id+"/collections/prices/sheet", nil)
	if st != http.StatusNotFound {
		t.Fatalf("missing link: %d %v", st, body)
	}
	// The key is entered on the administrator's settings page only; an
	// invalid key is refused there without breaking the page.
	tok := adminUI.csrf("/admin/settings")
	code, page := adminUI.post("/admin/settings/sheets", url.Values{"_csrf": {tok}, "service_account_json": {"{}"}})
	if code != http.StatusSeeOther && code != http.StatusOK {
		t.Fatalf("saving an invalid key: %d %.300s", code, page)
	}
	if strings.Contains(page, "private_key") {
		t.Fatal("the settings page echoes key material")
	}
}
