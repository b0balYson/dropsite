package mcp

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeCabinet records cabinet calls on top of the site fake.
type fakeCabinet struct {
	*fakeBackend
	access    AccessChange
	days      int
	removed   string
	revoked   []string
	counters  [2]*string
	rootRef   string
	rootClear string
	updates   UpdateFacts
	refreshed int
}

var cabinetTools = []string{"add_domain", "check_updates", "clear_root_site", "get_account", "get_counters", "get_site_stats",
	"list_connections", "list_domains", "remove_domain", "revoke_connection", "set_counters", "set_root_site",
	"set_site_access", "update_site"}

func (f *fakeCabinet) GetAccount(context.Context) (Account, error) {
	root := Site{ID: "abc", Title: "Placeholder"}
	return Account{Email: "a@x.io", Role: "admin", IsAdmin: true, SitesUsed: 3, SitesLimit: 10,
		MaxSiteSize: "50 MB", MaxFileSize: "10 MB", MaxFiles: 1000, KeepReleases: 5, DeployFilesMaxBytes: 1 << 20,
		RootSite: &root}, nil
}

func (f *fakeCabinet) CheckUpdates(_ context.Context, refresh bool) (UpdateFacts, error) {
	if refresh {
		f.refreshed++
	}
	return f.updates, nil
}

func (f *fakeCabinet) UpdateSite(_ context.Context, ref string, title, slug *string) (Site, error) {
	s, err := f.find(ref)
	if title != nil {
		s.Title = *title
	}
	if slug != nil {
		s.Slug = *slug
	}
	return s, err
}

func (f *fakeCabinet) SetSiteAccess(_ context.Context, ref string, c AccessChange) (SiteAccess, error) {
	f.access = c
	a := SiteAccess{SiteID: ref, PasswordProtected: c.Password != nil, HiddenFromSearch: c.HiddenFromSearch != nil && *c.HiddenFromSearch}
	if c.ExpiresAt != nil {
		a.LastDay = c.ExpiresAt.Add(-time.Nanosecond).Format(time.DateOnly)
	}
	return a, nil
}

func (f *fakeCabinet) ListDomains(_ context.Context, ref string) (DomainList, error) {
	return DomainList{SiteID: ref, Domains: []Domain{{Domain: "shop.example.com", Status: "pending"},
		{Domain: "old.example.com", Status: "rejected", RejectReason: "not yours"}}}, nil
}

func (f *fakeCabinet) AddDomain(_ context.Context, ref, name string) (DomainAdded, error) {
	return DomainAdded{SiteID: ref, Domain: Domain{Domain: name, Status: "pending"}, DNS: DNSSetup{Steps: "Create a CNAME."}}, nil
}

func (f *fakeCabinet) RemoveDomain(_ context.Context, _, name string) error {
	f.removed = name
	return nil
}

func (f *fakeCabinet) GetSiteStats(_ context.Context, ref string, days int) (SiteStats, error) {
	f.days = days
	day := DayViews{Date: "2026-11-10", Views: 40}
	return SiteStats{SiteID: ref, From: "2026-11-06", To: "2026-11-12", TotalViews: 120, TotalVisitors: 45,
		BusiestDay: &day, TopPages: []NamedViews{{Name: "/", Views: 90}},
		TopSources: []NamedViews{{Name: "", Views: 70}, {Name: "ya.ru", Views: 30}}}, nil
}

func (f *fakeCabinet) GetCounters(_ context.Context, ref string) (Counters, error) {
	return Counters{SiteID: ref, Global: CounterIDs{GoogleAnalyticsID: "G-GLOBAL"}}, nil
}

func (f *fakeCabinet) SetCounters(_ context.Context, ref string, metrica, ga *string) (Counters, error) {
	f.counters = [2]*string{metrica, ga}
	return Counters{SiteID: ref, Own: CounterIDs{YandexMetricaID: *metrica}, NewDeployID: "d9"}, nil
}

func (f *fakeCabinet) ListConnections(context.Context) ([]Connection, error) {
	return []Connection{{Kind: ConnApp, ID: 1, Name: "Chat App", Current: true}, {Kind: ConnToken, ID: 2, Name: "laptop"}}, nil
}

func (f *fakeCabinet) RevokeConnection(_ context.Context, kind string, id int64) error {
	f.revoked = append(f.revoked, kind+":"+strconv.FormatInt(id, 10))
	return nil
}

func (f *fakeCabinet) SetRootSite(_ context.Context, ref, confirm string) (RootSite, error) {
	f.rootRef = ref + "/" + confirm
	return RootSite{RootURL: "https://example.com/", Site: Site{ID: ref, Title: "Placeholder"}}, nil
}

func (f *fakeCabinet) ClearRootSite(_ context.Context, confirm string) (RootSite, error) {
	f.rootClear = confirm
	return RootSite{RootURL: "https://example.com/", Site: Site{ID: "abc", Title: "Placeholder"}}, nil
}

func toolNames(t *testing.T, cs *sdk.ClientSession) []string {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		ro := tool.Annotations.ReadOnlyHint
		destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		switch tool.Name {
		case "get_account", "list_domains", "get_site_stats", "get_counters", "list_connections", "check_updates":
			if !ro {
				t.Errorf("%s must be read-only", tool.Name)
			}
		case "remove_domain", "revoke_connection", "set_root_site", "clear_root_site":
			if !destructive {
				t.Errorf("%s must be destructive", tool.Name)
			}
		}
	}
	slices.Sort(names)
	return names
}

func TestCabinetToolsRegisteredOnlyWhenEnabled(t *testing.T) {
	f := &fakeCabinet{fakeBackend: newFake()}
	res := func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil }
	for _, name := range toolNames(t, connect(t, NewServer("t", res, Options{Folder: true}))) {
		if slices.Contains(cabinetTools, name) {
			t.Fatalf("%s registered without Options.Cabinet", name)
		}
	}
	names := toolNames(t, connect(t, NewServer("t", res, Options{Cabinet: true})))
	for _, want := range cabinetTools {
		if !slices.Contains(names, want) {
			t.Fatalf("%s missing: %v", want, names)
		}
	}
	for _, name := range names {
		if strings.Contains(name, "token") || strings.Contains(name, "code") || strings.HasPrefix(name, "create_conn") {
			t.Fatalf("no tool may issue credentials: %s", name)
		}
	}
}

func TestCabinetTools(t *testing.T) {
	f := &fakeCabinet{fakeBackend: newFake()}
	cs := connect(t, NewServer("t", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Cabinet: true}))

	out, isErr, _ := call(t, cs, "get_account", map[string]any{})
	if isErr || !strings.Contains(out["summary"].(string), "3 of 10 sites") || !strings.Contains(out["summary"].(string), "Placeholder") {
		t.Fatalf("account: %v", out)
	}

	if _, isErr, text := call(t, cs, "update_site", map[string]any{"site": "abc"}); !isErr || !strings.Contains(text, "nothing to change") {
		t.Fatalf("empty update: %q", text)
	}
	out, _, _ = call(t, cs, "update_site", map[string]any{"site": "abc", "title": "Coffee"})
	if out["title"] != "Coffee" || out["slug"] != "promo" {
		t.Fatalf("update title only: %v", out)
	}

	_, isErr, text := call(t, cs, "set_site_access", map[string]any{"site": "abc", "password": "p", "remove_password": true})
	if !isErr || !strings.Contains(text, "not both") {
		t.Fatalf("conflict: %q", text)
	}
	_, isErr, text = call(t, cs, "set_site_access", map[string]any{"site": "abc", "expires_on": "2026-12-31", "remove_expiry": true})
	if !isErr || !strings.Contains(text, "not both") {
		t.Fatalf("expiry conflict: %q", text)
	}
	_, isErr, text = call(t, cs, "set_site_access", map[string]any{"site": "abc", "expires_on": "31.12.2026"})
	if !isErr || !strings.Contains(text, "2026-12-31") {
		t.Fatalf("bad date: %q", text)
	}
	out, isErr, _ = call(t, cs, "set_site_access", map[string]any{"site": "abc", "password": "secret-pass", "expires_on": "2026-12-31"})
	if isErr || f.access.ExpiresAt == nil || !f.access.ExpiresAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("access: %v %+v", out, f.access)
	}
	if s := out["summary"].(string); !strings.Contains(s, "password") || !strings.Contains(s, "2026-12-31") || strings.Contains(s, "secret-pass") {
		t.Fatalf("access summary: %q", s)
	}

	out, _, _ = call(t, cs, "list_domains", map[string]any{"site": "abc"})
	if s := out["summary"].(string); !strings.Contains(s, "waits for an administrator") || !strings.Contains(s, "not yours") {
		t.Fatalf("domains summary: %q", s)
	}
	out, _, _ = call(t, cs, "add_domain", map[string]any{"site": "abc", "domain": "shop.example.com"})
	if s := out["summary"].(string); !strings.Contains(s, "approve") || !strings.Contains(s, "CNAME") {
		t.Fatalf("add domain: %v", out)
	}
	if _, isErr, text = call(t, cs, "remove_domain", map[string]any{"site": "abc", "domain": "shop.example.com", "confirm": "abc"}); !isErr ||
		!strings.Contains(text, "confirm") || f.removed != "" {
		t.Fatalf("unconfirmed remove: %q", text)
	}
	if _, isErr, _ = call(t, cs, "remove_domain", map[string]any{"site": "abc", "domain": "Shop.Example.com.", "confirm": "shop.example.com"}); isErr ||
		f.removed != "shop.example.com" {
		t.Fatalf("remove: %q", f.removed)
	}

	out, _, _ = call(t, cs, "get_site_stats", map[string]any{"site": "abc"})
	if f.days != DefaultStatsDays || !strings.Contains(out["summary"].(string), "Busiest day: 2026-11-10") ||
		!strings.Contains(out["summary"].(string), "ya.ru") {
		t.Fatalf("stats: %d %v", f.days, out)
	}
	_, _, _ = call(t, cs, "get_site_stats", map[string]any{"site": "abc", "days": 30})
	if f.days != 30 {
		t.Fatalf("days %d", f.days)
	}

	out, _, _ = call(t, cs, "get_counters", map[string]any{"site": "abc"})
	if s := out["summary"].(string); !strings.Contains(s, "no own") || !strings.Contains(s, "G-GLOBAL") {
		t.Fatalf("counters: %q", s)
	}
	if _, isErr, _ = call(t, cs, "set_counters", map[string]any{"site": "abc"}); !isErr {
		t.Fatal("empty counters change accepted")
	}
	out, _, _ = call(t, cs, "set_counters", map[string]any{"site": "abc", "yandex_metrica_id": "12345"})
	if f.counters[1] != nil || !strings.Contains(out["summary"].(string), "republished") {
		t.Fatalf("set counters: %v %v", f.counters, out)
	}

	out, _, _ = call(t, cs, "list_connections", map[string]any{})
	if s := out["summary"].(string); !strings.Contains(s, `"Chat App" (app, this assistant)`) || !strings.Contains(s, "API token") {
		t.Fatalf("connections: %q", s)
	}
	for _, args := range []map[string]any{
		{"kind": "app", "id": 1, "confirm": "laptop"},
		{"kind": "app", "id": 1, "confirm": ""},
		{"kind": "token", "id": 9, "confirm": "9"},
	} {
		if _, isErr, _ := call(t, cs, "revoke_connection", args); !isErr {
			t.Fatalf("revoke accepted: %v", args)
		}
	}
	if len(f.revoked) != 0 {
		t.Fatalf("revoked without confirmation: %v", f.revoked)
	}
	out, isErr, _ = call(t, cs, "revoke_connection", map[string]any{"kind": "app", "id": 1, "confirm": "chat app"})
	if isErr || out["was_current"] != true || !strings.Contains(out["summary"].(string), "reconnect") {
		t.Fatalf("revoke current: %v", out)
	}
	if _, isErr, _ = call(t, cs, "revoke_connection", map[string]any{"kind": "token", "id": 2, "confirm": "2"}); isErr || len(f.revoked) != 2 {
		t.Fatalf("revoke by id: %v", f.revoked)
	}

	out, _, _ = call(t, cs, "set_root_site", map[string]any{"site": "abc", "confirm": "abc"})
	if f.rootRef != "abc/abc" || !strings.Contains(out["summary"].(string), "https://example.com/ now shows") {
		t.Fatalf("set root: %q %v", f.rootRef, out)
	}
	out, _, _ = call(t, cs, "clear_root_site", map[string]any{"confirm": "abc"})
	if f.rootClear != "abc" || !strings.Contains(out["summary"].(string), "no longer shows") {
		t.Fatalf("clear root: %v", out)
	}
}

func TestCabinetWithoutSupport(t *testing.T) {
	cs := connect(t, NewServer("t", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return newFake(), nil },
		Options{Cabinet: true}))
	if _, isErr, text := call(t, cs, "get_account", map[string]any{}); !isErr || !strings.Contains(text, "web interface") {
		t.Fatalf("no cabinet: %q", text)
	}
	noLogin := errors.New("not connected: open the Connect AI page")
	cs = connect(t, NewServer("t", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return nil, noLogin },
		Options{Cabinet: true}))
	if _, isErr, text := call(t, cs, "list_connections", map[string]any{}); !isErr || !strings.Contains(text, "Connect AI") {
		t.Fatalf("resolver error: %q", text)
	}
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{512: "512 bytes", 1536: "1.5 KB", 50 << 20: "50 MB", 3 << 30: "3 GB"} {
		if got := HumanBytes(in); got != want {
			t.Errorf("%d: %q, want %q", in, got, want)
		}
	}
	if s := accessSummary(SiteAccess{}); s != "The site opens for everyone; search engines may index it; it has no expiry." {
		t.Fatalf("default access summary: %q", s)
	}
	if s := statsSummary(SiteStats{From: "a", To: "b"}); !strings.Contains(s, "No visits") {
		t.Fatalf("empty stats: %q", s)
	}
	if s := connectionsSummary(nil); !strings.Contains(s, "No connected") {
		t.Fatalf("no connections: %q", s)
	}
	if s := domainsSummary(nil); !strings.Contains(s, "no own domains") {
		t.Fatalf("no domains: %q", s)
	}
}
