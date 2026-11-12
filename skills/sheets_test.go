package skills

import (
	"strings"
	"testing"
)

// TestSheetsTopic keeps the Google Sheets rules the spec requires in the guide.
func TestSheetsTopic(t *testing.T) {
	text, ok := Guide("sheets")
	if !ok {
		t.Fatal("no sheets topic")
	}
	for _, want := range []string{
		"get_sheets_setup", "service_account_email", "Editor", "Viewer", `kind: "sheets"`, "link_sheet",
		"confirm", "source of truth", "overwritten", "Never ask anyone for the key", "two-way", "id",
		"interval_minutes", "/_dropsite/data/",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("sheets topic lacks %q", want)
		}
	}
}
