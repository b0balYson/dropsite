package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// Endpoints that give API clients (the local MCP server of the CLI) what
// the remote MCP tools offer.

func TestMeServerFacts(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	me := decodeAs[meDTO](t, h.do(http.MethodGet, "/api/v1/me", tok, nil))
	if me.MaxDomainsPerSite <= 0 || me.MaxInlineBytes != service.MaxInlineBytes || me.UIURL == "" || me.RootSite != nil {
		t.Fatalf("me %+v", me)
	}
	site := h.createSite(h.adminToken, "Root", "")
	wantStatus(t, h.do(http.MethodPut, "/api/v1/root-site", h.adminToken,
		map[string]string{"site": site.ID, "confirm": site.ID}), http.StatusOK)
	me = decodeAs[meDTO](t, h.do(http.MethodGet, "/api/v1/me", h.adminToken, nil))
	if me.RootSite == nil || me.RootSite.ID != site.ID {
		t.Fatalf("admin me %+v", me)
	}
	// Regular users never see it.
	if me := decodeAs[meDTO](t, h.do(http.MethodGet, "/api/v1/me", tok, nil)); me.RootSite != nil {
		t.Fatal("root site leaked to a user")
	}
}

func TestRootSiteNeedsAdminAndConfirm(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Bob", "bob-site")
	p := wantError(t, h.do(http.MethodPut, "/api/v1/root-site", tok,
		map[string]string{"site": site.ID, "confirm": site.ID}), http.StatusBadRequest, domain.CodeInvalidInput)
	if !strings.Contains(p.Message, "administrator") {
		t.Fatalf("user: %v", p)
	}
	p = wantError(t, h.do(http.MethodPut, "/api/v1/root-site", h.adminToken,
		map[string]string{"site": site.ID, "confirm": "other"}), http.StatusBadRequest, domain.CodeInvalidInput)
	if !strings.Contains(p.Message, "confirm") {
		t.Fatalf("confirm: %v", p)
	}
	// An administrator may choose any user's site, by slug too.
	rs := decodeAs[rootSiteDTO](t, h.do(http.MethodPut, "/api/v1/root-site", h.adminToken,
		map[string]string{"site": "bob-site", "confirm": "BOB-SITE"}))
	if rs.Site.ID != site.ID || rs.RootURL != "https://"+testenv.BaseDomain+"/" {
		t.Fatalf("set: %+v", rs)
	}
	wantError(t, h.do(http.MethodDelete, "/api/v1/root-site?confirm=nope", h.adminToken, nil),
		http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodDelete, "/api/v1/root-site?confirm="+site.ID, tok, nil),
		http.StatusBadRequest, domain.CodeInvalidInput)
	rs = decodeAs[rootSiteDTO](t, h.do(http.MethodDelete, "/api/v1/root-site?confirm="+site.ID, h.adminToken, nil))
	if rs.Site.ID != site.ID {
		t.Fatalf("clear: %+v", rs)
	}
}

func TestDomainSetupAndHostnamesInList(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	_, eve := h.user("eve@pp.test")
	site := h.createSite(tok, "Bob", "")
	ds := decodeAs[domainSetupDTO](t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/domain-setup", tok, nil))
	if ds.SiteID != site.ID || !strings.HasPrefix(ds.CNAMETarget, site.ID+".") || ds.MaxDomains <= 0 || ds.ServerIPs == nil {
		t.Fatalf("setup %+v", ds)
	}
	wantError(t, h.do(http.MethodGet, "/api/v1/sites/"+site.ID+"/domain-setup", eve, nil), http.StatusNotFound, domain.CodeNotFound)
	wantStatus(t, h.do(http.MethodPost, "/api/v1/sites/"+site.ID+"/hostnames", tok,
		map[string]string{"hostname": "shop.example.org"}), http.StatusCreated)
	l := decodeAs[list[siteItemDTO]](t, h.do(http.MethodGet, "/api/v1/sites", tok, nil))
	found := false
	for _, hn := range l.Items[0].Hostnames {
		found = found || (hn.Hostname == "shop.example.org" && hn.Kind == "external" && hn.Status == "pending")
	}
	if !found {
		t.Fatalf("hostnames %+v", l.Items[0].Hostnames)
	}
}

func TestCountersEndpoints(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	_, eve := h.user("eve@pp.test")
	site := h.createSite(tok, "Bob", "")
	path := "/api/v1/sites/" + site.ID + "/counters"
	c := decodeAs[countersDTO](t, h.do(http.MethodGet, path, tok, nil))
	if c.SiteID != site.ID || c.Own.YandexMetricaID != "" || c.NewDeployID != nil {
		t.Fatalf("get %+v", c)
	}
	wantError(t, h.do(http.MethodPatch, path, tok, `{}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantStatus(t, h.do(http.MethodPatch, path, tok, `{"yandex_metrica_id":"abc"}`), http.StatusBadRequest)
	wantError(t, h.do(http.MethodPatch, path, eve, `{"yandex_metrica_id":"123"}`), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodGet, path, eve, nil), http.StatusNotFound, domain.CodeNotFound)

	c = decodeAs[countersDTO](t, h.do(http.MethodPatch, path, tok, `{"yandex_metrica_id":"12345"}`))
	if c.Own.YandexMetricaID != "12345" || c.NewDeployID != nil {
		t.Fatalf("unpublished %+v", c)
	}
	wantStatus(t, h.deploy(site.ID, tok, testenv.Zip(t, index("<html><head></head><body>x</body></html>"))), http.StatusCreated)
	c = decodeAs[countersDTO](t, h.do(http.MethodPatch, path, tok, `{"google_analytics_id":"G-ABC123"}`))
	if c.Own.YandexMetricaID != "12345" || c.Own.GoogleAnalyticsID != "G-ABC123" || c.NewDeployID == nil {
		t.Fatalf("published %+v", c)
	}
	c = decodeAs[countersDTO](t, h.do(http.MethodPatch, path, tok, `{"yandex_metrica_id":""}`))
	if c.Own.YandexMetricaID != "" || c.Own.GoogleAnalyticsID != "G-ABC123" {
		t.Fatalf("removed %+v", c)
	}
}

func TestConnectionsEndpoints(t *testing.T) {
	h := newHarness(t)
	bob, tok := h.user("bob@pp.test")
	other := h.env.Token(bob, "laptop")
	_, eve := h.user("eve@pp.test")
	l := decodeAs[list[connectionDTO]](t, h.do(http.MethodGet, "/api/v1/connections", tok, nil))
	if l.Total != 2 {
		t.Fatalf("list %+v", l)
	}
	var mine, laptop connectionDTO
	for _, c := range l.Items {
		if c.Kind != connToken {
			t.Fatalf("kind %+v", c)
		}
		if c.Current {
			mine = c
		} else {
			laptop = c
		}
	}
	if mine.Name != "test" || laptop.Name != "laptop" {
		t.Fatalf("current %+v %+v", mine, laptop)
	}
	wantError(t, h.do(http.MethodDelete, "/api/v1/connections/token/"+laptop.ID, eve, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodDelete, "/api/v1/connections/app/"+laptop.ID, tok, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodDelete, "/api/v1/connections/other/"+laptop.ID, tok, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodDelete, "/api/v1/connections/token/x", tok, nil), http.StatusNotFound, domain.CodeNotFound)
	wantStatus(t, h.do(http.MethodDelete, "/api/v1/connections/token/"+laptop.ID, tok, nil), http.StatusNoContent)
	wantError(t, h.do(http.MethodGet, "/api/v1/me", other, nil), http.StatusUnauthorized, domain.CodeUnauthorized)
	if l := decodeAs[list[connectionDTO]](t, h.do(http.MethodGet, "/api/v1/connections", tok, nil)); l.Total != 1 {
		t.Fatalf("after revoke %+v", l)
	}
}

func TestSiteFilesEndpoints(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	_, eve := h.user("eve@pp.test")
	site := h.createSite(tok, "Bob", "")
	files := "/api/v1/sites/" + site.ID + "/files"
	wantStatus(t, h.do(http.MethodGet, files, tok, nil), http.StatusBadRequest)
	wantStatus(t, h.deploy(site.ID, tok, testenv.Zip(t, map[string]string{
		"index.html": "<h1>Price 10</h1>", "css/a.css": "body{}", "img/x.png": "\x89PNG\x00\x01",
	})), http.StatusCreated)

	l := decodeAs[siteFileListDTO](t, h.do(http.MethodGet, files+"?prefix=css/", tok, nil))
	if l.TotalFiles != 3 || len(l.Files) != 1 || l.Files[0].Path != "css/a.css" || !l.Current || l.DeployID == "" {
		t.Fatalf("list %+v", l)
	}
	wantError(t, h.do(http.MethodGet, files, eve, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodPost, files+"/read", eve, map[string]any{"paths": []string{"index.html"}}),
		http.StatusNotFound, domain.CodeNotFound)

	rd := decodeAs[siteFileReadDTO](t, h.do(http.MethodPost, files+"/read", tok,
		map[string]any{"paths": []string{"index.html", "img/*", "nope.txt"}}))
	if len(rd.Files) != 2 || rd.Files[0].Encoding == "" || len(rd.Missing) != 1 || rd.DeployID != l.DeployID {
		t.Fatalf("read %+v", rd)
	}
	var sawText, sawBinary bool
	for _, f := range rd.Files {
		sawText = sawText || (f.Path == "index.html" && f.Encoding == "utf8" && f.Content == "<h1>Price 10</h1>")
		sawBinary = sawBinary || (f.Path == "img/x.png" && f.Encoding == "base64")
	}
	if !sawText || !sawBinary {
		t.Fatalf("contents %+v", rd.Files)
	}
	wantError(t, h.do(http.MethodPost, files+"/read", tok, map[string]any{"paths": []string{}}),
		http.StatusBadRequest, domain.CodeInvalidInput)

	upd := map[string]any{"base_deploy_id": l.DeployID, "files": []map[string]string{{"path": "index.html", "content": "<h1>Price 12</h1>"}},
		"delete": []string{"css/"}}
	wantError(t, h.do(http.MethodPatch, files, eve, upd), http.StatusNotFound, domain.CodeNotFound)
	res := decodeAs[filesResultDTO](t, h.do(http.MethodPatch, files, tok, upd))
	if res.Written != 1 || res.Deleted != 1 || res.BaseDeployID != l.DeployID || res.Deploy.FileCount != 2 || res.Deploy.Source != "mcp" {
		t.Fatalf("update %+v", res)
	}
	// A stale base is a conflict.
	wantError(t, h.do(http.MethodPatch, files, tok, upd), http.StatusConflict, domain.CodeDeployConflict)
	// Bodies may carry 1 MB of content, but not much more.
	big := map[string]any{"base_deploy_id": res.DeployID, "files": []map[string]string{{"path": "a.txt", "content": strings.Repeat("a", 5<<20)}}}
	p := wantError(t, h.do(http.MethodPatch, files, tok, big), http.StatusBadRequest, domain.CodeInvalidInput)
	if !strings.Contains(p.Message, "too large") {
		t.Fatalf("big body %v", p)
	}
	over := map[string]any{"base_deploy_id": res.DeployID, "files": []map[string]string{{"path": "a.txt", "content": strings.Repeat("a", 2<<20)}}}
	wantStatus(t, h.do(http.MethodPatch, files, tok, over), http.StatusRequestEntityTooLarge)
}

func TestUploadEndpoints(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	_, eve := h.user("eve@pp.test")
	up := decodeAs[uploadDTO](t, h.do(http.MethodPost, "/api/v1/uploads", tok, map[string]string{"title": "Big"}))
	if up.ID == "" || up.Mode != service.UploadReplace || up.MaxCallBytes != service.MaxInlineBytes {
		t.Fatalf("begin %+v", up)
	}
	part := map[string]any{"files": []map[string]string{{"path": "index.html", "content": "<p>hi</p>"}}}
	// Another user's session does not exist for them.
	wantError(t, h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/files", eve, part), http.StatusBadRequest, domain.CodeUploadExpired)
	up = decodeAs[uploadDTO](t, h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/files", tok, part))
	if up.Files != 1 || up.Bytes == 0 {
		t.Fatalf("files %+v", up)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/commit", eve, nil), http.StatusBadRequest, domain.CodeUploadExpired)
	rec := h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/commit", tok, nil)
	wantStatus(t, rec, http.StatusCreated)
	res := decodeAs[filesResultDTO](t, rec)
	if res.Site.Title != "Big" || res.Written != 1 || res.URL == "" {
		t.Fatalf("commit %+v", res)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/uploads/"+up.ID+"/commit", tok, nil), http.StatusBadRequest, domain.CodeUploadExpired)
	wantError(t, h.do(http.MethodPost, "/api/v1/uploads", tok, map[string]string{"mode": "patch"}),
		http.StatusBadRequest, domain.CodeInvalidInput)
}

func TestFormSettingsAndSearchEndpoints(t *testing.T) {
	f := newFormHarness(t)
	_, eve := f.user("eve@pp.test")
	settings := "/api/v1/sites/" + f.site.ID + "/forms/settings"
	st := decodeAs[formSiteSettingsDTO](t, f.do(http.MethodGet, settings, f.owner, nil))
	if st.SiteID != f.site.ID || st.SiteURL == "" || st.CabinetURL == "" || st.FormNames == nil || st.RequireAltcha {
		t.Fatalf("settings %+v", st)
	}
	wantError(t, f.do(http.MethodGet, settings, eve, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, f.do(http.MethodPatch, settings, eve, `{"require_altcha":true}`), http.StatusNotFound, domain.CodeNotFound)

	for _, body := range []string{"name=Ann&phone=111", "name=Bob&phone=222", "name=Cid&phone=333"} {
		if rec := f.visitor(http.MethodPost, "/_form/lead", formCT, body, browser()); rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Fatalf("submit %d", rec.Code)
		}
	}
	search := "/api/v1/sites/" + f.site.ID + "/forms/submissions/search"
	p := decodeAs[submissionSearchDTO](t, f.do(http.MethodGet, search+"?limit=2&form=lead&days=7", f.owner, nil))
	if p.Total != 3 || len(p.Items) != 2 || p.NextCursor == nil || p.CabinetURL == "" || p.ServerTime.IsZero() {
		t.Fatalf("search %+v", p)
	}
	p2 := decodeAs[submissionSearchDTO](t, f.do(http.MethodGet, search+"?limit=2&cursor="+*p.NextCursor, f.owner, nil))
	if len(p2.Items) != 1 || p2.NextCursor != nil {
		t.Fatalf("page 2 %+v", p2)
	}
	if p := decodeAs[submissionSearchDTO](t, f.do(http.MethodGet, search+"?until=2000-01-01", f.owner, nil)); p.Total != 0 {
		t.Fatalf("until %+v", p)
	}
	for _, q := range []string{"limit=0", "limit=101", "days=0", "since=yesterday", "cursor=bad"} {
		wantError(t, f.do(http.MethodGet, search+"?"+q, f.owner, nil), http.StatusBadRequest, domain.CodeInvalidInput)
	}
	wantError(t, f.do(http.MethodGet, search, eve, nil), http.StatusNotFound, domain.CodeNotFound)

	export := "/api/v1/sites/" + f.site.ID + "/forms/submissions/export"
	e := decodeAs[submissionExportDTO](t, f.do(http.MethodGet, export+"?max_rows=2", f.owner, nil))
	if e.Rows != 2 || e.Total != 3 || !e.Truncated || !strings.Contains(e.CSV, "created_at") {
		t.Fatalf("export %+v", e)
	}
	wantError(t, f.do(http.MethodGet, export+"?max_rows=1001", f.owner, nil), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, f.do(http.MethodGet, export, eve, nil), http.StatusNotFound, domain.CodeNotFound)
	st = decodeAs[formSiteSettingsDTO](t, f.do(http.MethodGet, settings, f.owner, nil))
	if len(st.FormNames) != 1 || st.FormNames[0] != "lead" {
		t.Fatalf("names %+v", st.FormNames)
	}
}
