package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/api"
	"github.com/b0balYson/dropsite/internal/mcp/remote"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/sheets/sheetstest"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func newSheetsParityRig(t *testing.T, fake *sheetstest.Fake) *parityRig {
	t.Helper()
	hooks := &hookSender{}
	env := testenv.New(t, testenv.WithFormSender(hooks), testenv.WithSheetsTransport(fake))
	r := &parityRig{t: t, env: env, hooks: hooks}
	r.api = httptest.NewServer(api.New(env.Svc, nil))
	t.Cleanup(r.api.Close)
	r.remote = httptest.NewServer(remote.New(env.Svc, remote.Options{}))
	t.Cleanup(r.remote.Close)
	return r
}

func TestStdioSheetsTools(t *testing.T) {
	const doc = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"
	fake := &sheetstest.Fake{}
	r := newSheetsParityRig(t, fake)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)

	setup := same(t, local, web, "get_sheets_setup", map[string]any{})
	if setup["configured"] != false || !strings.Contains(setup["next_step"].(string), "administrator") {
		t.Fatalf("unconfigured setup: %v", setup)
	}
	v, err := r.env.Svc.CreateSite(ctx, ann, "Cafe", "cafe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.env.Svc.CreateCollection(ctx, ann, v.Site.ID, service.CollectionInput{Name: "prices",
		Schema: json.RawMessage(`{"type":"object","properties":{"price":{"type":"number"}}}`)}); err != nil {
		t.Fatal(err)
	}
	link := map[string]any{"site": "cafe", "collection": "prices",
		"url": "https://docs.google.com/spreadsheets/d/" + doc + "/edit#gid=0", "confirm": "prices"}
	same(t, local, web, "link_sheet", link)
	wantToolErr(t, local, "link_sheet", link, "administrator")

	if _, err := r.env.Svc.SetSheetsServiceAccount(ctx, r.env.Admin, string(sheetstest.Key())); err != nil {
		t.Fatal(err)
	}
	setup = same(t, local, web, "get_sheets_setup", map[string]any{})
	if setup["service_account_email"] != sheetstest.Email || !strings.Contains(setup["next_step"].(string), sheetstest.Email) {
		t.Fatalf("setup: %v", setup)
	}
	fake.Add(doc, &sheetstest.Tab{ID: 0, Title: "Prices", Rows: [][]string{{"name", "price"}, {"Tea", "90,5"}}})

	noConfirm := map[string]any{"site": "cafe", "collection": "prices", "url": link["url"]}
	same(t, local, web, "link_sheet", noConfirm)
	wantToolErr(t, local, "link_sheet", noConfirm, "confirm")

	out := mustTool(t, local, "link_sheet", link)
	if out["last_status"] != "ok" || out["last_rows"] != float64(1) || !strings.Contains(out["summary"].(string), "1 row") {
		t.Fatalf("link_sheet: %v", out)
	}
	got := same(t, local, web, "get_sheet_link", map[string]any{"site": "cafe", "collection": "prices"})
	if got["sheet_title"] != "Prices" || got["service_account_email"] != sheetstest.Email {
		t.Fatalf("get_sheet_link: %v", got)
	}
	fake.SetRows(doc, 0, [][]string{{"name", "price"}, {"Tea", "cheap"}})
	synced := same(t, local, web, "sync_sheet", map[string]any{"site": "cafe", "collection": "prices"})
	if synced["last_status"] != "failed" || !strings.Contains(synced["summary"].(string), "Row 2, column price") {
		t.Fatalf("sync_sheet: %v", synced)
	}
	mustTool(t, local, "unlink_sheet", map[string]any{"site": "cafe", "collection": "prices"})
	same(t, local, web, "get_sheet_link", map[string]any{"site": "cafe", "collection": "prices"})
	wantToolErr(t, local, "sync_sheet", map[string]any{"site": "cafe", "collection": "prices"}, "not found")

	// Another user's site does not exist for this user.
	bob := r.env.User("bob@pp.test")
	bv, err := r.env.Svc.CreateSite(ctx, bob, "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]map[string]any{
		"get_sheet_link": {"site": bv.Site.ID, "collection": "x"},
		"sync_sheet":     {"site": bv.Site.ID, "collection": "x"},
		"unlink_sheet":   {"site": bv.Site.ID, "collection": "x"},
		"link_sheet":     {"site": bv.Site.ID, "collection": "x", "url": link["url"], "confirm": "x"},
	} {
		same(t, local, web, name, a)
		wantToolErr(t, local, name, a, "not found")
	}

	// Form submissions to a sheet through add_channel.
	same(t, local, web, "add_channel", map[string]any{"site": "cafe", "kind": "sheets", "target": "https://example.com/x"})
	added := mustTool(t, local, "add_channel", map[string]any{"site": "cafe", "kind": "sheets", "target": link["url"]})
	if c := added["channel"].(map[string]any); c["state"] != "active" || !strings.Contains(added["next_step"].(string), "Google Sheet") {
		t.Fatalf("add_channel sheets: %v", added)
	}
}
