package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/skills"
)

func updatesSession(t *testing.T, f *fakeCabinet) *sdk.ClientSession {
	t.Helper()
	return connect(t, NewServer("t", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Cabinet: true}))
}

func TestCheckUpdates(t *testing.T) {
	notes := "https://github.com/b0balYson/dropsite/releases/tag/v0.5.0"
	cases := []struct {
		name      string
		facts     UpdateFacts
		wantIn    []string
		wantNotIn []string
		panel     bool
		command   bool
	}{
		{"admin with button", UpdateFacts{Current: "0.4.0", Latest: "0.5.0", Available: true, ReleaseURL: notes,
			CheckEnabled: true, IsAdmin: true, HelperPresent: true, PanelURL: "https://admin.example.com/admin/settings#updates-h"},
			[]string{"0.5.0 is available", notes, "https://admin.example.com/admin/settings#updates-h"}, nil, true, false},
		{"admin without helper", UpdateFacts{Current: "0.4.0", Latest: "0.5.0", Available: true, ReleaseURL: notes,
			CheckEnabled: true, IsAdmin: true, PanelURL: "http://localhost:8082/admin/settings#updates-h"},
			[]string{"ssh <server> dropsite-update"}, []string{"localhost:8082"}, false, true},
		{"user", UpdateFacts{Current: "0.4.0", Latest: "0.5.0", Available: true, ReleaseURL: notes, CheckEnabled: true},
			[]string{"Ask your dropsite administrator", notes}, []string{"dropsite-update", "/admin/settings"}, false, false},
		{"up to date", UpdateFacts{Current: "0.5.0", Latest: "0.5.0", ReleaseURL: notes, CheckEnabled: true, IsAdmin: true,
			HelperPresent: true, PanelURL: "https://p/admin/settings#updates-h"},
			[]string{"runs dropsite 0.5.0, the latest release"}, nil, true, false},
		{"check off", UpdateFacts{Current: "dev+abc"}, []string{"does not check"}, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCabinet{fakeBackend: newFake(), updates: tc.facts}
			out, isErr, text := call(t, updatesSession(t, f), "check_updates", map[string]any{})
			if isErr {
				t.Fatal(text)
			}
			all := out["summary"].(string) + " " + out["how_to_update"].(string)
			for _, w := range tc.wantIn {
				if !strings.Contains(all, w) {
					t.Errorf("missing %q in %q", w, all)
				}
			}
			for _, w := range tc.wantNotIn {
				if strings.Contains(all, w) {
					t.Errorf("unexpected %q in %q", w, all)
				}
			}
			if out["update_available"] != tc.facts.Available || out["current_version"] != tc.facts.Current {
				t.Errorf("facts %v", out)
			}
			if (out["update_panel_url"] != nil) != tc.panel || (out["update_command"] != nil) != tc.command {
				t.Errorf("panel/command %v", out)
			}
		})
	}
}

func TestCheckUpdatesRefreshPassesThrough(t *testing.T) {
	f := &fakeCabinet{fakeBackend: newFake(), updates: UpdateFacts{Current: "0.4.0", IsAdmin: true}}
	cs := updatesSession(t, f)
	call(t, cs, "check_updates", map[string]any{})
	call(t, cs, "check_updates", map[string]any{"refresh": true})
	if f.refreshed != 1 {
		t.Fatalf("refreshed %d", f.refreshed)
	}
}

func TestGetAccountReportsUpdate(t *testing.T) {
	f := &fakeCabinet{fakeBackend: newFake()}
	cs := updatesSession(t, f)
	out, _, _ := call(t, cs, "get_account", map[string]any{})
	if out["update_available"] != false || out["latest_version"] != nil {
		t.Fatalf("no update: %v", out)
	}
}

// No MCP tool may start an update: the only update tool reads, and no tool
// name offers to install, upgrade or apply anything.
func TestNoMCPToolStartsAnUpdate(t *testing.T) {
	f := &fakeCabinet{fakeBackend: newFake()}
	all := Options{Files: true, Folder: true, SiteFiles: true, Forms: true, Cabinet: true, Collections: true,
		Workflow: true, Drafts: true}
	cs := connect(t, NewServer("t", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil }, all))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		n := tool.Name
		if strings.Contains(n, "upgrade") || strings.Contains(n, "install") || n == "update_server" ||
			n == "request_update" || n == "apply_update" {
			t.Errorf("tool %s looks like it updates the server", n)
		}
		if n == "check_updates" && !tool.Annotations.ReadOnlyHint {
			t.Error("check_updates must be read-only")
		}
	}
}

func TestGuideTellsToMentionUpdatesOnce(t *testing.T) {
	if !strings.Contains(Instructions, "update_available") || !strings.Contains(Instructions, "once per chat") ||
		!strings.Contains(Instructions, "check_updates") {
		t.Fatalf("instructions miss the update rule:\n%s", Instructions)
	}
	g, _ := skills.Guide("account")
	for _, want := range []string{"check_updates", "once per conversation", "You cannot update the server"} {
		if !strings.Contains(g, want) {
			t.Errorf("account guide misses %q", want)
		}
	}
	ov, _ := skills.Guide("overview")
	if !strings.Contains(ov, "updates of the dropsite server") {
		t.Error("overview does not point to the update rule")
	}
}
