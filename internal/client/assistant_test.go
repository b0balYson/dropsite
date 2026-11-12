package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/api"
	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// The client methods behind the local MCP tools, against the real API.

func realAPI(t *testing.T) (*testenv.Env, string) {
	t.Helper()
	env := testenv.New(t)
	srv := httptest.NewServer(api.New(env.Svc, nil))
	t.Cleanup(srv.Close)
	return env, srv.URL
}

func apiErr(t *testing.T, err error, status int) {
	t.Helper()
	var ae *client.APIError
	if !errors.As(err, &ae) || ae.Status != status {
		t.Fatalf("error %v, want HTTP %d", err, status)
	}
}

func TestClientCabinetMethods(t *testing.T) {
	env, url := realAPI(t)
	ctx := context.Background()
	ann := env.User("ann@pp.test")
	c := client.New(url, env.Token(ann, "cli"))
	site, err := c.CreateSite(ctx, client.CreateSiteRequest{Title: "Ann", Slug: "ann"})
	if err != nil {
		t.Fatal(err)
	}
	me, err := c.Me(ctx)
	if err != nil || me.MaxInlineBytes == 0 || me.UIURL == "" || me.RootSite != nil {
		t.Fatalf("me %+v %v", me, err)
	}
	if ds, err := c.DomainSetup(ctx, "ann"); err != nil || ds.SiteID != site.ID || ds.CNAMETarget == "" {
		t.Fatalf("setup %+v %v", ds, err)
	}
	if st, err := c.SiteStats(ctx, "ann", 3); err != nil || len(st.Days) != 3 {
		t.Fatalf("stats %+v %v", st, err)
	}
	id := "12345"
	cs, err := c.UpdateCounters(ctx, "ann", client.CountersUpdate{YandexMetricaID: &id})
	if err != nil || cs.Own.YandexMetricaID != "12345" {
		t.Fatalf("counters %+v %v", cs, err)
	}
	if cs, err := c.Counters(ctx, "ann"); err != nil || cs.Own.YandexMetricaID != "12345" {
		t.Fatalf("get counters %+v %v", cs, err)
	}
	future := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	off := false
	s, err := c.UpdateSite(ctx, "ann", client.UpdateSiteRequest{Protected: &off, ExpiresAt: &client.Expiry{At: &future}})
	if err != nil || s.ExpiresAt == nil {
		t.Fatalf("expiry %+v %v", s, err)
	}
	if s, err = c.UpdateSite(ctx, "ann", client.UpdateSiteRequest{ExpiresAt: &client.Expiry{}}); err != nil || s.ExpiresAt != nil {
		t.Fatalf("no expiry %+v %v", s, err)
	}
	l, err := c.ListSites(ctx, client.ListOptions{})
	if err != nil || len(l.Items) != 1 || len(l.Items[0].Hostnames) == 0 {
		t.Fatalf("sites %+v %v", l, err)
	}

	laptop := env.Token(ann, "laptop")
	conns, err := c.ListConnections(ctx)
	if err != nil || conns.Total != 2 {
		t.Fatalf("connections %+v %v", conns, err)
	}
	for _, cn := range conns.Items {
		if cn.Name == "laptop" {
			if err := c.RevokeConnection(ctx, cn.Kind, cn.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err = client.New(url, laptop).Me(ctx)
	apiErr(t, err, http.StatusUnauthorized)
	apiErr(t, c.RevokeConnection(ctx, "token", "../../me"), http.StatusNotFound)
	// A redirect (here from a ".." segment) is never followed.
	apiErr(t, c.RevokeConnection(ctx, "..", "1"), http.StatusTemporaryRedirect)

	_, err = c.SetRootSite(ctx, "ann", "ann")
	apiErr(t, err, http.StatusBadRequest)
	admin := client.New(url, env.Token(env.Admin, "cli"))
	if _, err := admin.SetRootSite(ctx, "ann", "nope"); err == nil {
		t.Fatal("root without confirm")
	}
	rs, err := admin.SetRootSite(ctx, "ann", "ann")
	if err != nil || rs.Site.ID != site.ID || rs.RootURL == "" {
		t.Fatalf("root %+v %v", rs, err)
	}
	if me, err := admin.Me(ctx); err != nil || me.RootSite == nil || me.RootSite.ID != site.ID {
		t.Fatalf("admin me %+v %v", me, err)
	}
	if rs, err := admin.ClearRootSite(ctx, site.ID); err != nil || rs.Site.ID != site.ID {
		t.Fatalf("clear %+v %v", rs, err)
	}
}

func TestClientFormsMethods(t *testing.T) {
	env, url := realAPI(t)
	ctx := context.Background()
	ann := env.User("ann@pp.test")
	c := client.New(url, env.Token(ann, "cli"))
	if _, err := c.CreateSite(ctx, client.CreateSiteRequest{Title: "Cafe", Slug: "cafe"}); err != nil {
		t.Fatal(err)
	}
	st, err := c.SetRequireAltcha(ctx, "cafe", true)
	if err != nil || !st.RequireAltcha || st.CabinetURL == "" {
		t.Fatalf("settings %+v %v", st, err)
	}
	if st, err = c.FormSettings(ctx, "cafe"); err != nil || !st.RequireAltcha || st.FormNames == nil {
		t.Fatalf("get settings %+v %v", st, err)
	}
	ch, err := c.AddChannel(ctx, "cafe", "webhook", "https://hooks.example.com/x")
	if err != nil || ch.Secret == "" {
		t.Fatalf("channel %+v %v", ch, err)
	}
	if l, err := c.ListChannels(ctx, "cafe"); err != nil || l.Total != 1 {
		t.Fatalf("channels %+v %v", l, err)
	}
	if got, err := c.SetChannelEnabled(ctx, "cafe", ch.Channel.ID, false); err != nil || got.Enabled {
		t.Fatalf("disable %+v %v", got, err)
	}
	if r, err := c.RotateChannelSecret(ctx, "cafe", ch.Channel.ID); err != nil || r.Secret == ch.Secret {
		t.Fatalf("rotate %+v %v", r, err)
	}
	_, err = c.VerifyChannel(ctx, "cafe", ch.Channel.ID, "123456")
	apiErr(t, err, http.StatusBadRequest)
	f := client.SubmissionFilter{Form: "lead", Query: "x", Since: time.Unix(0, 0), Until: time.Now().Add(time.Hour), Days: 30, Limit: 5}
	if p, err := c.SearchSubmissions(ctx, "cafe", f); err != nil || p.Total != 0 || p.Items == nil || p.NextCursor != nil {
		t.Fatalf("search %+v %v", p, err)
	}
	if e, err := c.ExportSubmissions(ctx, "cafe", f, 10); err != nil || e.Rows != 0 || e.CabinetURL == "" {
		t.Fatalf("export %+v %v", e, err)
	}
	if err := c.DeleteChannel(ctx, "cafe", ch.Channel.ID); err != nil {
		t.Fatal(err)
	}
}

func TestClientSiteFilesMethods(t *testing.T) {
	env, url := realAPI(t)
	ctx := context.Background()
	ann := env.User("ann@pp.test")
	c := client.New(url, env.Token(ann, "cli"))
	up, err := c.BeginUpload(ctx, client.UploadStart{Title: "Shop"})
	if err != nil || up.ID == "" {
		t.Fatalf("begin %+v %v", up, err)
	}
	html := "<h1>" + strings.Repeat("<b>&amp;</b>", 1000) + "</h1>"
	if up, err = c.UploadFiles(ctx, up.ID, []client.InlineFile{{Path: "index.html", Content: html}}, nil); err != nil || up.Files != 1 {
		t.Fatalf("upload %+v %v", up, err)
	}
	res, err := c.CommitUpload(ctx, up.ID, nil)
	if err != nil || res.Written != 1 || res.Site.ID == "" {
		t.Fatalf("commit %+v %v", res, err)
	}
	ref := res.Site.ID
	l, err := c.ListSiteFiles(ctx, ref, "", "")
	if err != nil || l.TotalFiles != 1 || l.DeployID != res.DeployID {
		t.Fatalf("list %+v %v", l, err)
	}
	rd, err := c.ReadSiteFiles(ctx, ref, l.DeployID, []string{"index.html"})
	if err != nil || len(rd.Files) != 1 || rd.Files[0].Content != html || rd.Files[0].Encoding != "utf8" {
		t.Fatalf("read %+v %v", rd, err)
	}
	upd, err := c.UpdateSiteFiles(ctx, ref, client.FilesChange{BaseDeployID: l.DeployID,
		Files: []client.InlineFile{{Path: "logo.png", Content: "iVBORw0KGgo=", Encoding: "base64"}}})
	if err != nil || upd.Written != 1 || upd.BaseDeployID != l.DeployID || upd.Deploy.FileCount != 2 {
		t.Fatalf("update %+v %v", upd, err)
	}
	_, err = c.UpdateSiteFiles(ctx, ref, client.FilesChange{BaseDeployID: l.DeployID, Delete: []string{"logo.png"}})
	apiErr(t, err, http.StatusConflict)
	_, err = client.New(url, env.Token(env.User("bob@pp.test"), "cli")).ListSiteFiles(ctx, ref, "", "")
	apiErr(t, err, http.StatusNotFound)
}
