package skills

import (
	"strings"
	"testing"
)

// TestReferencesTopic keeps the "inspired by, never a copy" rules in the
// guide and the link from the create topic.
func TestReferencesTopic(t *testing.T) {
	text, ok := Guide("references")
	if !ok {
		t.Fatal("no references topic")
	}
	for _, want := range []string{"inspect_reference", "palette", "typography", "density", "composition", "mood",
		"Never copy", "logos", "trademarks", "not a copy", "screenshot", "robots.txt", "dropsite-reference"} {
		if !strings.Contains(text, want) {
			t.Errorf("references topic lacks %q", want)
		}
	}
	create, _ := Guide("create")
	if !strings.Contains(create, "`references`") {
		t.Error("create does not point to the references topic")
	}
	overview, _ := Guide("overview")
	if !strings.Contains(overview, "`references`") {
		t.Error("overview does not list the references topic")
	}
}
