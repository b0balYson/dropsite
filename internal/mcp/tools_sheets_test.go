package mcp

import (
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// No sheet tool may accept the service account key.
func TestSheetToolsTakeNoKey(t *testing.T) {
	for _, build := range []func() (*jsonschema.Schema, error){
		func() (*jsonschema.Schema, error) { return jsonschema.For[linkSheetArgs](nil) },
		func() (*jsonschema.Schema, error) { return jsonschema.For[sheetCollectionArgs](nil) },
	} {
		schema, err := build()
		if err != nil {
			t.Fatal(err)
		}
		for name := range schema.Properties {
			if n := strings.ToLower(name); strings.Contains(n, "secret") || strings.Contains(n, "key") || strings.Contains(n, "token") {
				t.Errorf("tool input has a %q property", name)
			}
		}
	}
}

func TestSheetSummaryAndSetup(t *testing.T) {
	l := SheetLink{Collection: "prices", URL: "https://docs.google.com/spreadsheets/d/x/edit#gid=0", SheetTitle: "Prices",
		IntervalMinutes: 15, NextSyncAt: "2026-11-12T10:15:00Z", LastSyncAt: "2026-11-12T10:00:00Z", LastStatus: "failed",
		LastMessage: "2 row(s) have problems", Errors: []SheetRowError{{Row: 3, Field: "price", Message: "not a number"},
			{Row: 4, Message: "too large"}}}
	s := SheetLinkSummary(l)
	for _, want := range []string{"Prices", "every 15 minutes", "previous data", "Row 3, column price: not a number", "Row 4: too large", "overwritten"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary %q lacks %q", s, want)
		}
	}
	l.LastStatus, l.LastRows, l.IntervalMinutes = "ok", 12, 0
	if s := SheetLinkSummary(l); !strings.Contains(s, "12 row(s)") || !strings.Contains(s, "only on request") {
		t.Errorf("ok summary %q", s)
	}
	if n := SheetsSetupNextStep(SheetsSetup{}); !strings.Contains(n, "administrator") {
		t.Errorf("unconfigured %q", n)
	}
	if n := SheetsSetupNextStep(SheetsSetup{Configured: true, ServiceAccountEmail: "a@b.iam.gserviceaccount.com"}); !strings.Contains(n, "a@b.iam.gserviceaccount.com") {
		t.Errorf("configured %q", n)
	}
}
