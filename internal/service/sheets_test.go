package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/sheets/sheetstest"
	"github.com/b0balYson/dropsite/internal/store"
)

const testDoc = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"

func docURL(gid string) string {
	return "https://docs.google.com/spreadsheets/d/" + testDoc + "/edit#gid=" + gid
}

func sheetsEnv(t *testing.T, configure bool) (*env, *sheetstest.Fake) {
	t.Helper()
	e := newEnv(t)
	f := &sheetstest.Fake{}
	e.svc.sheetsTransport = f
	if configure {
		if _, err := e.svc.SetSheetsServiceAccount(context.Background(), e.admin, string(sheetstest.Key())); err != nil {
			t.Fatal(err)
		}
	}
	return e, f
}

func TestSheetsServiceAccount(t *testing.T) {
	ctx := context.Background()
	e, f := sheetsEnv(t, false)
	ann := e.user("ann@pp.test")
	st, err := e.svc.GetSheetsStatus(ctx)
	if err != nil || st.Configured || st.Email != "" || st.Unreadable {
		t.Fatalf("initial status %+v %v", st, err)
	}
	wantNotFound(t, func() error { _, err := e.svc.SetSheetsServiceAccount(ctx, ann, string(sheetstest.Key())); return err }())
	viaMCP := e.admin
	viaMCP.ViaMCP = true
	wantNotFound(t, func() error {
		_, err := e.svc.SetSheetsServiceAccount(ctx, viaMCP, string(sheetstest.Key()))
		return err
	}())
	_, err = e.svc.SetSheetsServiceAccount(ctx, e.admin, `{"type":"service_account"}`)
	wantCode(t, err, domain.CodeSheetsInvalid)

	f.TokenStatus = http.StatusBadRequest
	_, err = e.svc.SetSheetsServiceAccount(ctx, e.admin, string(sheetstest.Key()))
	wantCode(t, err, domain.CodeSheetsInvalid)
	f.TokenStatus = 0

	st, err = e.svc.SetSheetsServiceAccount(ctx, e.admin, "  "+string(sheetstest.Key())+"\n")
	if err != nil || !st.Configured || st.Email != sheetstest.Email {
		t.Fatalf("configured %+v %v", st, err)
	}
	stored, err := e.repo.GetSettings(ctx)
	if err != nil || strings.Contains(stored[SheetsSettingKey], "PRIVATE KEY") || stored[SheetsSettingKey] == "" {
		t.Fatalf("key stored in clear: %v", err)
	}
	rows, _, err := e.svc.ListAudit(ctx, e.admin, store.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "PRIVATE KEY") {
			t.Fatalf("key in the audit log: %s", b)
		}
	}
	if err := e.svc.ClearSheetsServiceAccount(ctx, ann); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("user cleared the key: %v", err)
	}
	if err := e.svc.ClearSheetsServiceAccount(ctx, e.admin); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.GetSheetsStatus(ctx); st.Configured {
		t.Fatal("still configured after clear")
	}
}

func TestSheetsFormChannel(t *testing.T) {
	ctx := context.Background()
	e, f := sheetsEnv(t, false)
	e.svc.senders = &fakeSender{fail: map[string][]error{}}
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	host := v.Site.ID + "." + testBase

	_, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelSheets, docURL("5"))
	wantCode(t, err, domain.CodeSheetsNotConfigured)
	if _, err := e.svc.SetSheetsServiceAccount(ctx, e.admin, string(sheetstest.Key())); err != nil {
		t.Fatal(err)
	}
	f.Add(testDoc, &sheetstest.Tab{ID: 0, Title: "Other"}, &sheetstest.Tab{ID: 5, Title: "Leads"})

	_, err = e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelSheets, "https://example.com/sheet")
	wantCode(t, err, domain.CodeSheetsInvalidURL)
	f.ReadOnly(testDoc, true)
	_, err = e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelSheets, docURL("5"))
	wantCode(t, err, domain.CodeSheetsAccessDenied)
	if !strings.Contains(err.Error(), sheetstest.Email) || !strings.Contains(err.Error(), "Editor") {
		t.Fatalf("access error does not explain what to do: %v", err)
	}
	f.ReadOnly(testDoc, false)

	res, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelSheets, docURL("5"))
	if err != nil || res.Channel.Status != domain.ChannelActive || res.Channel.Target != docURL("5") {
		t.Fatalf("channel %+v %v", res.Channel, err)
	}
	header := f.Rows(testDoc, 5)
	if len(header) != 1 || len(header[0]) != 2 {
		t.Fatalf("header not written: %v", header)
	}

	submit := func(fields ...string) {
		t.Helper()
		p := post(host, fields...)
		p.Origin = "https://" + host
		if _, err := e.svc.SubmitForm(ctx, p); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Minute)
		if n, err := e.svc.DeliverDueForms(ctx); err != nil || n != 1 {
			t.Fatalf("deliver: %d %v", n, err)
		}
	}
	submit("name", "Ann", "phone", "=HYPERLINK(\"x\")")
	submit("name", "Bob", "promo", "SALE")
	rows := f.Rows(testDoc, 5)
	if len(rows) != 3 {
		t.Fatalf("rows %v", rows)
	}
	wantHeader := []string{header[0][0], header[0][1], "name", "phone", "promo"}
	if !reflect.DeepEqual(rows[0], wantHeader) {
		t.Fatalf("header %v, want %v", rows[0], wantHeader)
	}
	if rows[1][1] != "lead" || rows[1][2] != "Ann" || rows[1][3] != "=HYPERLINK(\"x\")" || rows[1][0] == "" {
		t.Fatalf("first row %v", rows[1])
	}
	if rows[2][2] != "Bob" || rows[2][3] != "" || rows[2][4] != "SALE" {
		t.Fatalf("second row %v", rows[2])
	}

	// Lost access: the delivery is retried and counted by the breaker.
	f.Deny(testDoc, true)
	p := post(host, "name", "Cid")
	p.Origin = "https://" + host
	if _, err := e.svc.SubmitForm(ctx, p); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	if _, err := e.svc.DeliverDueForms(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := e.repo.GetFormChannel(ctx, v.Site.ID, res.Channel.ID)
	if err != nil || ch.ConsecutiveFailures != 1 {
		t.Fatalf("breaker not fed: %+v %v", ch, err)
	}
	subs, _, err := e.svc.ListFormSubmissions(ctx, ann, v.Site.ID, "", Page{})
	if err != nil {
		t.Fatal(err)
	}
	d := subs[0].Deliveries[0]
	if d.Status != domain.DeliveryPending || !strings.Contains(d.LastError, sheetstest.Email) {
		t.Fatalf("delivery %+v", d)
	}
}

const priceSchema = `{"type":"object","properties":{"name":{"type":"string"},"price":{"type":"number"},
	"qty":{"type":"integer"},"tags":{"type":"array","items":{"type":"string"}},"hit":{"type":"boolean"}},
	"required":["name"],"additionalProperties":false}`

func mkPrices(t *testing.T, e *env, a Actor, siteID string, mode domain.CollectionMode, name string) {
	t.Helper()
	if _, err := e.svc.CreateCollection(context.Background(), a, siteID, CollectionInput{Name: name, Mode: mode,
		Schema: json.RawMessage(priceSchema)}); err != nil {
		t.Fatal(err)
	}
}

func docsOf(t *testing.T, e *env, a Actor, siteID, name string) map[string]map[string]any {
	t.Helper()
	p, err := e.svc.ListDocuments(context.Background(), a, siteID, name, DocumentQuery{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, d := range p.Items {
		var m map[string]any
		if err := json.Unmarshal(d.Data, &m); err != nil {
			t.Fatal(err)
		}
		out[d.ID] = m
	}
	return out
}

func TestSheetLinkAndSync(t *testing.T) {
	ctx := context.Background()
	e, f := sheetsEnv(t, true)
	ann := e.user("ann@pp.test")
	bob := e.user("bob@pp.test")
	v := mkFormSite(t, e, ann)
	site := v.Site.ID
	mkPrices(t, e, ann, site, domain.CollectionPublic, "prices")
	mkPrices(t, e, ann, site, domain.CollectionModerated, "reviews")
	f.Add(testDoc, &sheetstest.Tab{ID: 0, Title: "Prices", Rows: [][]string{
		{"name", "price", "qty", "tags", "hit", ""},
		{"Coffee", "1 290,50", "3", "hot, new", "да"},
		{},
		{"Tea", "90", "", "", "no"},
	}})

	_, _, err := e.svc.PutSheetLink(ctx, ann, site, "reviews", SheetLinkInput{URL: docURL("0")})
	wantCode(t, err, domain.CodeSheetsInvalid)
	_, _, err = e.svc.PutSheetLink(ctx, ann, site, "prices", SheetLinkInput{URL: docURL("0"), IntervalMinutes: 3})
	wantCode(t, err, domain.CodeSheetsInvalid)
	_, _, err = e.svc.PutSheetLink(ctx, bob, site, "prices", SheetLinkInput{URL: docURL("0")})
	wantNotFound(t, err)
	_, _, err = e.svc.PutSheetLink(ctx, ann, site, "prices", SheetLinkInput{URL: docURL("9")})
	wantCode(t, err, domain.CodeSheetsAccessDenied)

	view, created, err := e.svc.PutSheetLink(ctx, ann, site, "prices", SheetLinkInput{URL: docURL("0"), IntervalMinutes: 15})
	if err != nil || !created || view.Link.LastStatus != domain.SheetSyncOK || view.Link.LastRows != 2 ||
		view.ServiceAccountEmail != sheetstest.Email || view.URL != docURL("0") || view.Link.SheetTitle != "Prices" {
		t.Fatalf("link %+v %v %v", view, created, err)
	}
	docs := docsOf(t, e, ann, site, "prices")
	want := map[string]map[string]any{
		"row-2": {"name": "Coffee", "price": 1290.5, "qty": float64(3), "tags": []any{"hot", "new"}, "hit": true},
		"row-4": {"name": "Tea", "price": float64(90), "hit": false},
	}
	if !reflect.DeepEqual(docs, want) {
		t.Fatalf("documents %v", docs)
	}

	// A bad row keeps the documents and reports the row.
	f.SetRows(testDoc, 0, [][]string{{"name", "price"}, {"Coffee", "200"}, {"Cake", "expensive"}, {"", "5"}})
	view, err = e.svc.SyncSheet(ctx, ann, site, "prices")
	if err != nil || view.Link.LastStatus != domain.SheetSyncFailed || len(view.Link.LastErrors) != 2 {
		t.Fatalf("sync with bad rows %+v %v", view.Link, err)
	}
	if e0 := view.Link.LastErrors[0]; e0.Row != 3 || e0.Field != "price" {
		t.Fatalf("row error %+v", e0)
	}
	if e1 := view.Link.LastErrors[1]; e1.Row != 4 || e1.Field != "name" {
		t.Fatalf("row error %+v", e1)
	}
	if got := docsOf(t, e, ann, site, "prices"); !reflect.DeepEqual(got, want) {
		t.Fatalf("documents changed by a failed sync: %v", got)
	}

	// Access lost: the result says what to do, documents stay.
	f.Deny(testDoc, true)
	view, err = e.svc.SyncSheet(ctx, ann, site, "prices")
	if err != nil || view.Link.LastStatus != domain.SheetSyncFailed || !strings.Contains(view.Link.LastMessage, sheetstest.Email) {
		t.Fatalf("denied sync %+v %v", view.Link, err)
	}
	f.Deny(testDoc, false)

	// Periodic synchronization.
	f.SetRows(testDoc, 0, [][]string{{"ID", "name"}, {"latte", "Latte"}})
	if n, err := e.svc.SyncDueSheets(ctx); err != nil || n != 0 {
		t.Fatalf("not due yet: %d %v", n, err)
	}
	e.clock.Advance(16 * time.Minute)
	if n, err := e.svc.SyncDueSheets(ctx); err != nil || n != 1 {
		t.Fatalf("due: %d %v", n, err)
	}
	got := docsOf(t, e, ann, site, "prices")
	if !reflect.DeepEqual(got, map[string]map[string]any{"latte": {"name": "Latte"}}) {
		t.Fatalf("after schedule %v", got)
	}
	view, err = e.svc.GetSheetLink(ctx, ann, site, "prices")
	if err != nil || view.Link.LastTrigger != domain.SheetTriggerSchedule {
		t.Fatalf("trigger %+v %v", view.Link, err)
	}

	// Unlinking keeps the documents; a second unlink is not found.
	if err := e.svc.DeleteSheetLink(ctx, ann, site, "prices"); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, e.svc.DeleteSheetLink(ctx, ann, site, "prices"))
	if got := docsOf(t, e, ann, site, "prices"); len(got) != 1 {
		t.Fatalf("documents after unlink %v", got)
	}
	_, err = e.svc.GetSheetLink(ctx, bob, site, "prices")
	wantNotFound(t, err)
}

func TestSheetOpsRateLimited(t *testing.T) {
	ctx := context.Background()
	e, f := sheetsEnv(t, true)
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)
	mkPrices(t, e, ann, v.Site.ID, domain.CollectionPublic, "prices")
	f.Add(testDoc, &sheetstest.Tab{ID: 0, Title: "S", Rows: [][]string{{"name"}, {"A"}}})
	if _, _, err := e.svc.PutSheetLink(ctx, ann, v.Site.ID, "prices", SheetLinkInput{URL: docURL("0")}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < SheetOpsPerSite; i++ {
		if _, err := e.svc.SyncSheet(ctx, ann, v.Site.ID, "prices"); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	_, err := e.svc.SyncSheet(ctx, ann, v.Site.ID, "prices")
	wantCode(t, err, domain.CodeRateLimited)
}

func TestSheetDocumentsRules(t *testing.T) {
	c := domain.Collection{Name: "x"}
	res := sheetDocuments(c, nil, [][]string{{"a", " a "}, {"1", "2"}})
	if res.failed != 1 || res.errs[0].Row != 1 {
		t.Fatalf("duplicate header: %+v", res)
	}
	res = sheetDocuments(c, nil, nil)
	if res.failed != 1 {
		t.Fatalf("empty sheet: %+v", res)
	}
	res = sheetDocuments(c, nil, [][]string{{"id", "n"}, {"a", "1"}, {"A", "2"}, {"Bad id!", "3"}})
	if res.failed != 2 || res.errs[0].Row != 3 || res.errs[1].Row != 4 {
		t.Fatalf("ids: %+v", res)
	}
	res = sheetDocuments(c, nil, [][]string{{"n", "", "m"}, {"1", "skip", " x "}, {"", ""}})
	if res.failed != 0 || len(res.docs) != 1 || string(res.docs[0].Data) != `{"m":"x","n":"1"}` || *res.docs[0].Position != 2 {
		t.Fatalf("plain: %+v", res)
	}
	many := [][]string{{"n"}}
	for i := 0; i <= MaxDocumentsPerCollection; i++ {
		many = append(many, []string{"v"})
	}
	if res := sheetDocuments(c, nil, many); res.failed != 1 || res.errs[0].Row != 0 {
		t.Fatalf("too many rows: %+v", res.errs)
	}
	for in, want := range map[string]float64{"1 290,50": 1290.5, "1,290.50": 1290.5, "-3": -3, "1e3": 1000} {
		if got, ok := sheetNumber(in); !ok || got != want {
			t.Errorf("number %q: %v %v", in, got, ok)
		}
	}
	if _, ok := sheetNumber("abc"); ok {
		t.Error("abc is a number")
	}
	if sheetValue("integer", "2,5") != "2,5" || sheetValue("boolean", "maybe") != "maybe" {
		t.Error("bad values must stay strings")
	}
}

func TestSheetFixedColumnsPerLanguage(t *testing.T) {
	if got := baseHeader(nil, ownerLang("zh")); !reflect.DeepEqual(got, []string{"日期", "表单"}) {
		t.Fatalf("zh header %v", got)
	}
	// A header written in another language is recognized, not duplicated.
	if got := baseHeader([]string{"Дата", "Форма", "name"}, "zh"); len(got) != 3 {
		t.Fatalf("header grew %v", got)
	}
	if ownerLang("de") != "ru" || ownerLang("zh") != "zh" {
		t.Fatal("owner language fallback")
	}
	if sheetValue("boolean", "是") != true || sheetValue("boolean", "否") != false {
		t.Fatal("chinese booleans")
	}
}
