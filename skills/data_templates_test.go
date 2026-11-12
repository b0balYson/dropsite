package skills

import (
	"strings"
	"testing"
)

// TestDataTopicTemplates keeps the collection template rules of the spec in
// the data topic.
func TestDataTopicTemplates(t *testing.T) {
	text, ok := Guide("data")
	if !ok {
		t.Fatal("no data topic")
	}
	for _, want := range []string{
		"list_collection_templates", "`template`", "with_samples", "`menu`", "`reviews`",
		"starting point, not a design", "site's own CSS", "fallback content", "200",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("data topic lacks %q", want)
		}
	}
}
