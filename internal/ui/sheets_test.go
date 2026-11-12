package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/sheets/sheetstest"
	"github.com/b0balYson/dropsite/internal/testenv"
)

const uiSheetDoc = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"

func TestSheetsAdminSettings(t *testing.T) {
	h := newHarness(t, testenv.WithSheetsTransport(&sheetstest.Fake{}))
	h.loginAdmin()
	page := h.get("/admin/settings")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page.body, `id="sheets-h"`, "Google Таблицы", "Не подключено", `name="service_account_json"`)

	p := h.submit("/admin/settings", "/admin/settings/sheets", url.Values{"service_account_json": {`{"type":"user"}`}})
	wantStatus(t, p, http.StatusSeeOther)
	wantContains(t, h.follow(p).body, "Настройки Google Таблиц не сохранены")

	p = h.submit("/admin/settings", "/admin/settings/sheets", url.Values{"service_account_json": {string(sheetstest.Key())}})
	shown := h.follow(p)
	wantContains(t, shown.body, "Подключено", sheetstest.Email, "Удалить ключ")
	wantNotContains(t, shown.body, "PRIVATE KEY", "private_key")

	p = h.submit("/admin/settings", "/admin/settings/sheets", url.Values{"clear": {"1"}})
	wantContains(t, h.follow(p).body, "Ключ удалён", "Не подключено")
}

func TestSheetsCollectionAndForms(t *testing.T) {
	fake := &sheetstest.Fake{}
	h := newHarness(t, testenv.WithSheetsTransport(fake))
	ctx := context.Background()
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Cafe", "")
	if _, err := h.env.Svc.CreateCollection(ctx, alice, v.Site.ID, service.CollectionInput{Name: "prices"}); err != nil {
		t.Fatal(err)
	}
	base := "/sites/" + v.Site.ID + "/data/prices"
	wantContains(t, h.get(base).body, `id="sheet-h"`, "Google Таблицы не подключены")
	wantContains(t, h.get("/sites/"+v.Site.ID+"/forms").body, "Google Таблица", "Google Таблицы не подключены")

	if _, err := h.env.Svc.SetSheetsServiceAccount(ctx, h.env.Admin, string(sheetstest.Key())); err != nil {
		t.Fatal(err)
	}
	fake.Add(uiSheetDoc, &sheetstest.Tab{ID: 0, Title: "Prices", Rows: [][]string{{"name", "price"}, {"Tea", "90"}}})
	link := "https://docs.google.com/spreadsheets/d/" + uiSheetDoc + "/edit#gid=0"

	page := h.get(base)
	wantContains(t, page.body, sheetstest.Email, "Связать и синхронизировать", "перезаписаны")
	p := h.submit(base, base+"/sheet", url.Values{"url": {link}, "interval_minutes": {"15"}})
	wantStatus(t, p, http.StatusSeeOther)
	wantContains(t, h.follow(p).body, "Таблица связана, на сайте строк: 1", "каждые 15 мин", "Prices")

	fake.SetRows(uiSheetDoc, 0, [][]string{{"name", "name"}})
	p = h.submit(base, base+"/sheet/sync", nil)
	failed := h.follow(p)
	wantContains(t, failed.body, "Ничего не изменилось", "sheet-errors", "used twice")

	p = h.submit(base, base+"/sheet/delete", nil)
	wantContains(t, h.follow(p).body, "Таблица отвязана")

	forms := "/sites/" + v.Site.ID + "/forms"
	wantContains(t, h.get(forms).body, "Подключить таблицу", sheetstest.Email)
	p = h.submit(forms, forms+"/channels", url.Values{"kind": {"sheets"}, "target": {link}})
	wantStatus(t, p, http.StatusSeeOther)
	chs, err := h.env.Svc.ListFormChannels(ctx, alice, v.Site.ID)
	if err != nil || len(chs) != 1 || chs[0].Kind != domain.ChannelSheets || chs[0].Status != domain.ChannelActive {
		t.Fatalf("channels %+v %v", chs, err)
	}
	wantContains(t, h.follow(p).body, uiSheetDoc)

	// Another user sees nothing.
	h.submit(forms, "/logout", nil)
	h.loginUser("bob@pp.test")
	wantStatus(t, h.submit("/sites", base+"/sheet/sync", nil), http.StatusNotFound)
}
