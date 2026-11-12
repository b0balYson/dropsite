package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// formsFake implements FormsBackend on top of the site fake.
type formsFake struct {
	*fakeBackend
	filter  SubmissionFilter
	rows    int
	deleted []string
	rotated []string
}

func (f *formsFake) GetFormSettings(_ context.Context, ref string) (FormSettings, error) {
	s, err := f.find(ref)
	return FormSettings{SiteID: s.ID, SiteURL: s.URL}, err
}

func (f *formsFake) SetFormSettings(_ context.Context, ref string, on bool) (FormSettings, error) {
	s, err := f.find(ref)
	return FormSettings{SiteID: s.ID, RequireAltcha: on}, err
}

func (f *formsFake) ListSubmissions(_ context.Context, _ string, flt SubmissionFilter) (SubmissionsPage, error) {
	f.filter = flt
	return SubmissionsPage{Total: 0}, nil
}

func (f *formsFake) ExportSubmissions(_ context.Context, _ string, flt SubmissionFilter, rows int) (SubmissionsExport, error) {
	f.filter, f.rows = flt, rows
	return SubmissionsExport{CSV: "created_at,form,host\n"}, nil
}

func (f *formsFake) ListChannels(_ context.Context, ref string) (ChannelList, error) {
	if _, err := f.find(ref); err != nil {
		return ChannelList{}, err
	}
	return ChannelList{Channels: []Channel{
		{ID: "7", Kind: "email", Target: "Ann@Example.com", State: "active"},
		{ID: "8", Kind: "telegram", State: "waiting_for_confirmation"},
	}}, nil
}

func (f *formsFake) AddChannel(_ context.Context, _, kind, target string) (ChannelAdded, error) {
	return ChannelAdded{Channel: Channel{ID: "9", Kind: kind, Target: target}, NextStep: "ask"}, nil
}

func (f *formsFake) VerifyChannel(_ context.Context, _, id, code string) (Channel, error) {
	return Channel{ID: id, Summary: code}, nil
}

func (f *formsFake) SetChannelEnabled(_ context.Context, _, id string, on bool) (Channel, error) {
	return Channel{ID: id, Enabled: on}, nil
}

func (f *formsFake) DeleteChannel(_ context.Context, _, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *formsFake) RotateChannelSecret(_ context.Context, _, id string) (ChannelAdded, error) {
	f.rotated = append(f.rotated, id)
	return ChannelAdded{Channel: Channel{ID: id}, Secret: "s"}, nil
}

func toolNames(t *testing.T, cs *sdk.ClientSession) map[string]*sdk.Tool {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*sdk.Tool{}
	for _, tool := range list.Tools {
		out[tool.Name] = tool
	}
	return out
}

var formTools = []string{"add_channel", "delete_channel", "export_submissions", "get_form_settings", "list_channels",
	"list_submissions", "rotate_channel_secret", "set_channel_enabled", "set_form_settings", "verify_channel"}

func TestFormsToolsRegisteredOnlyWithOption(t *testing.T) {
	f := &formsFake{fakeBackend: newFake()}
	res := func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil }
	tools := toolNames(t, connect(t, NewServer("test", res, Options{Folder: true})))
	for _, n := range formTools {
		if tools[n] != nil {
			t.Fatalf("%s registered without Forms", n)
		}
	}
	tools = toolNames(t, connect(t, NewServer("test", res, Options{Files: true, Forms: true})))
	for _, n := range formTools {
		tool := tools[n]
		if tool == nil {
			t.Fatalf("%s missing", n)
		}
		if strings.HasPrefix(n, "list_") || strings.HasPrefix(n, "get_") || n == "export_submissions" {
			if !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s not read-only", n)
			}
		}
	}
	for _, n := range []string{"delete_channel", "rotate_channel_secret"} {
		if d := tools[n].Annotations.DestructiveHint; d == nil || !*d {
			t.Errorf("%s not destructive", n)
		}
	}
}

func TestFormsToolsArguments(t *testing.T) {
	f := &formsFake{fakeBackend: newFake()}
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Forms: true}))

	out, isErr, _ := call(t, cs, "get_form_settings", map[string]any{"site": "promo"})
	if isErr || !strings.Contains(out["form_snippet"].(string), `action="/_form/contact"`) ||
		!strings.Contains(out["form_snippet"].(string), `name="_hp"`) || out["form_names"] == nil {
		t.Fatalf("settings: %v", out)
	}
	if _, isErr, text := call(t, cs, "get_form_settings", map[string]any{"site": "nope"}); !isErr || !strings.Contains(text, "list_sites") {
		t.Fatalf("missing site: %q", text)
	}
	out, _, _ = call(t, cs, "set_form_settings", map[string]any{"site": "promo", "require_altcha": true})
	if out["require_altcha"] != true {
		t.Fatalf("set: %v", out)
	}

	out, isErr, _ = call(t, cs, "list_submissions", map[string]any{"site": "promo", "since": "2026-05-01",
		"until": "2026-05-07", "query": "Ann", "form": " lead ", "limit": 5, "cursor": "c"})
	want := SubmissionFilter{Form: "lead", Query: "Ann", Since: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC), Cursor: "c", Limit: 5}
	if isErr || f.filter != want || out["submissions"] == nil {
		t.Fatalf("filter %+v, out %v", f.filter, out)
	}
	call(t, cs, "list_submissions", map[string]any{"site": "promo", "since": "2026-05-01T10:00:00+02:00", "days": 7})
	if !f.filter.Since.Equal(time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)) || f.filter.Days != 7 {
		t.Fatalf("rfc3339 %+v", f.filter)
	}
	for _, args := range []map[string]any{{"since": "yesterday"}, {"until": "05/01/2026"}, {"days": -1}, {"days": 99999}} {
		args["site"] = "promo"
		if _, isErr, text := call(t, cs, "list_submissions", args); !isErr || text == "" {
			t.Errorf("accepted %v", args)
		}
	}
	out, isErr, _ = call(t, cs, "export_submissions", map[string]any{"site": "promo", "days": 30, "max_rows": 50})
	if isErr || f.rows != 50 || f.filter.Days != 30 || out["csv"] == "" {
		t.Fatalf("export %v", out)
	}
	if _, isErr, text := call(t, cs, "export_submissions", map[string]any{"site": "promo", "max_rows": 5000}); !isErr || !strings.Contains(text, "cabinet_url") {
		t.Fatalf("too many rows: %q", text)
	}

	out, _, _ = call(t, cs, "add_channel", map[string]any{"site": "promo", "kind": " Telegram "})
	if out["channel"].(map[string]any)["kind"] != "telegram" {
		t.Fatalf("add: %v", out)
	}
	out, _, _ = call(t, cs, "verify_channel", map[string]any{"site": "promo", "channel_id": "8", "code": " 123456 "})
	if out["summary"] != "123456" {
		t.Fatalf("verify: %v", out)
	}
	out, _, _ = call(t, cs, "set_channel_enabled", map[string]any{"site": "promo", "channel_id": "7", "enabled": true})
	if out["enabled"] != true {
		t.Fatalf("enable: %v", out)
	}
}

func TestFormsToolsConfirm(t *testing.T) {
	f := &formsFake{fakeBackend: newFake()}
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Forms: true}))
	for _, c := range []map[string]any{
		{"channel_id": "7", "confirm": ""},
		{"channel_id": "7", "confirm": "8"},
		{"channel_id": "8", "confirm": ""},
	} {
		c["site"] = "promo"
		if _, isErr, text := call(t, cs, "delete_channel", c); !isErr || !strings.Contains(text, "confirm") {
			t.Fatalf("unconfirmed %v: %q", c, text)
		}
		if _, isErr, _ := call(t, cs, "rotate_channel_secret", c); !isErr {
			t.Fatalf("unconfirmed rotate %v", c)
		}
	}
	if _, isErr, text := call(t, cs, "delete_channel", map[string]any{"site": "promo", "channel_id": "99", "confirm": "99"}); !isErr || !strings.Contains(text, "list_channels") {
		t.Fatalf("missing channel: %q", text)
	}
	if len(f.deleted)+len(f.rotated) != 0 {
		t.Fatal("changed without confirmation")
	}
	out, isErr, _ := call(t, cs, "delete_channel", map[string]any{"site": "promo", "channel_id": "7", "confirm": "ann@example.com"})
	if isErr || out["deleted"] != "7" || len(f.deleted) != 1 {
		t.Fatalf("delete: %v", out)
	}
	out, isErr, _ = call(t, cs, "rotate_channel_secret", map[string]any{"site": "promo", "channel_id": "8", "confirm": "8"})
	if isErr || out["secret"] != "s" || len(f.rotated) != 1 {
		t.Fatalf("rotate: %v", out)
	}

	// A backend without forms support reports it.
	cs = connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return newFake(), nil },
		Options{Forms: true}))
	full := map[string]map[string]any{
		"add_channel":           {"kind": "email"},
		"delete_channel":        {"channel_id": "7", "confirm": "7"},
		"rotate_channel_secret": {"channel_id": "7", "confirm": "7"},
		"set_channel_enabled":   {"channel_id": "7", "enabled": true},
		"set_form_settings":     {"require_altcha": true},
		"verify_channel":        {"channel_id": "7", "code": "1"},
	}
	for _, n := range formTools {
		args := map[string]any{"site": "promo"}
		for k, v := range full[n] {
			args[k] = v
		}
		if _, isErr, text := call(t, cs, n, args); !isErr || !strings.Contains(text, "web interface") {
			t.Errorf("%s without support: %q", n, text)
		}
	}
}
