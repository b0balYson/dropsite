package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/skills"
)

func TestGetGuide(t *testing.T) {
	notConnected := errors.New("not connected")
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) {
		return nil, notConnected
	}, Options{Files: true, Instructions: Instructions}))
	if cs.InitializeResult().Instructions != skills.Instructions {
		t.Fatal("instructions not sent")
	}
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var guide *sdk.Tool
	for _, tool := range list.Tools {
		if tool.Name == "get_guide" {
			guide = tool
		}
	}
	if guide == nil || !guide.Annotations.ReadOnlyHint {
		t.Fatalf("get_guide missing or not read-only: %+v", guide)
	}
	schema, _ := guide.InputSchema.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	topic, _ := props["topic"].(map[string]any)
	enum, _ := topic["enum"].([]any)
	if len(enum) != len(skills.Topics())+1 || !slices.Contains(enum, any("all")) || !slices.Contains(enum, any("forms")) {
		t.Fatalf("topic enum: %v", topic)
	}

	// Works without a backend: the guide is useful before connecting.
	for _, name := range append(skills.Topics(), "all") {
		out, isErr, text := call(t, cs, "get_guide", map[string]any{"topic": name})
		want, _ := skills.Guide(name)
		if isErr || out["guide"] != want || out["topic"] != name {
			t.Fatalf("%s: %v %q", name, isErr, text)
		}
	}
	out, isErr, _ := call(t, cs, "get_guide", map[string]any{})
	if all, _ := skills.Guide("all"); isErr || out["guide"] != all {
		t.Fatal("default topic is not all")
	}
	if _, isErr, text := call(t, cs, "get_guide", map[string]any{"topic": "weather"}); !isErr ||
		!strings.Contains(text, "overview") {
		t.Fatalf("unknown topic: %v %q", isErr, text)
	}
}

// TestDescriptionsPointToGuide keeps tool descriptions plain and linked to
// the guide for clients that drop server instructions.
func TestDescriptionsPointToGuide(t *testing.T) {
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) {
		return newFake(), nil
	}, Options{Files: true}))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if len(tool.Description) < 60 {
			t.Errorf("%s: description too short to guide the model", tool.Name)
		}
		if tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("%s: no title annotation", tool.Name)
		}
		switch tool.Name {
		case "list_sites", "deploy_files":
			if !strings.Contains(tool.Description, "get_guide") {
				t.Errorf("%s does not point to get_guide", tool.Name)
			}
		case "delete_site":
			if !strings.Contains(tool.Description, "confirm") {
				t.Error("delete_site does not ask for confirmation")
			}
		}
	}
}
