package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/skills"
)

// allTools lists the tools of a server with every tool group, as the remote
// server offers them.
func allTools(t *testing.T) []*sdk.Tool {
	t.Helper()
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) {
		return newFake(), nil
	}, Options{Files: true, SiteFiles: true, Forms: true, Cabinet: true, Collections: true, Workflow: true, Drafts: true}))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return list.Tools
}

// schemaMap decodes a tool schema into a generic map.
func schemaMap(t *testing.T, s any) map[string]any {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func properties(t *testing.T, s any) map[string]any {
	t.Helper()
	p, _ := schemaMap(t, s)["properties"].(map[string]any)
	return p
}

// destructiveWithoutConfirm are the destructive tools that predate the
// confirm parameter; their descriptions still demand the user's explicit yes
// (see docs/mcp-tools.md).
var destructiveWithoutConfirm = []string{"save_documents", "delete_documents", "moderate_documents"}

// TestToolCatalogRules pins the conventions of docs/mcp-tools.md across all
// tools, so tools added later follow them.
func TestToolCatalogRules(t *testing.T) {
	for _, tool := range allTools(t) {
		in := properties(t, tool.InputSchema)
		for name, p := range in {
			if d, _ := p.(map[string]any)["description"].(string); d == "" {
				t.Errorf("%s.%s has no description", tool.Name, name)
			}
		}
		destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		_, hasConfirm := in["confirm"]
		if hasConfirm && !destructive {
			t.Errorf("%s takes confirm but is not marked destructive", tool.Name)
		}
		if destructive && !hasConfirm && !slices.Contains(destructiveWithoutConfirm, tool.Name) {
			t.Errorf("%s is destructive but takes no confirm", tool.Name)
		}
		if destructive && !strings.Contains(tool.Description, "confirm") {
			t.Errorf("%s is destructive but its description does not ask for confirmation", tool.Name)
		}
		if tool.Annotations.ReadOnlyHint && destructive {
			t.Errorf("%s is both read-only and destructive", tool.Name)
		}
		if out := properties(t, tool.OutputSchema); out["attached"] != nil && tool.Name != "get_site" {
			t.Errorf("%s carries the attachments schema of get_site", tool.Name)
		}
	}
}

func TestClosedChoicesAreEnumerated(t *testing.T) {
	want := map[string]map[string][]any{
		"begin_upload":     {"mode": {"", "replace", "patch"}},
		"screenshot_site":  {"device": {"", "desktop", "mobile"}},
		"compare_versions": {"device": {"", "desktop", "mobile"}},
	}
	for _, tool := range allTools(t) {
		for prop, values := range want[tool.Name] {
			p, _ := properties(t, tool.InputSchema)[prop].(map[string]any)
			if got, _ := p["enum"].([]any); !slices.Equal(got, values) {
				t.Errorf("%s.%s enum = %v, want %v", tool.Name, prop, got, values)
			}
		}
	}
}

// TestGuideCoversEveryTool keeps the guide and the tool list in step: every
// tool is explained somewhere in the guide, and the overview routes to every
// topic.
func TestGuideCoversEveryTool(t *testing.T) {
	all, _ := skills.Guide(skills.TopicAll)
	for _, tool := range allTools(t) {
		if tool.Name != "get_guide" && !strings.Contains(all, tool.Name) {
			t.Errorf("the guide never mentions %s", tool.Name)
		}
	}
	overview, _ := skills.Guide("overview")
	for _, topic := range skills.Topics() {
		if topic != "overview" && !strings.Contains(overview, "`"+topic+"`") {
			t.Errorf("the overview does not route to topic %s", topic)
		}
	}
}
