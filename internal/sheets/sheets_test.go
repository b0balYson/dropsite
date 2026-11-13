package sheets_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/sheets"
	"github.com/b0balYson/dropsite/internal/sheets/sheetstest"
)

const docID = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"

func TestParseURL(t *testing.T) {
	tests := []struct {
		in      string
		gid     int64
		hasGID  bool
		wantErr bool
	}{
		{in: "https://docs.google.com/spreadsheets/d/" + docID + "/edit#gid=123", gid: 123, hasGID: true},
		{in: "https://docs.google.com/spreadsheets/d/" + docID + "/edit?gid=0#gid=0", gid: 0, hasGID: true},
		{in: "https://docs.google.com/spreadsheets/d/" + docID},
		{in: "  https://docs.google.com/a/example.com/spreadsheets/d/" + docID + "/edit  "},
		{in: "http://docs.google.com/spreadsheets/d/" + docID, wantErr: true},
		{in: "https://evil.com/spreadsheets/d/" + docID, wantErr: true},
		{in: "https://docs.google.com:8443/spreadsheets/d/" + docID, wantErr: true},
		{in: "https://docs.google.com/document/d/" + docID, wantErr: true},
		{in: "https://docs.google.com/spreadsheets/d/short", wantErr: true},
		{in: "https://docs.google.com/spreadsheets/d/" + docID + "/edit#gid=abc", wantErr: true},
		{in: docID, wantErr: true},
	}
	for _, tt := range tests {
		ref, err := sheets.ParseURL(tt.in)
		if (err != nil) != tt.wantErr {
			t.Fatalf("%q: err %v", tt.in, err)
		}
		if err != nil {
			continue
		}
		if ref.SpreadsheetID != docID || (ref.GID != nil) != tt.hasGID || (tt.hasGID && *ref.GID != tt.gid) {
			t.Fatalf("%q: got %+v", tt.in, ref)
		}
	}
	ref, _ := sheets.ParseURL("https://docs.google.com/spreadsheets/d/" + docID + "/edit#gid=7")
	if got := ref.URL(); got != "https://docs.google.com/spreadsheets/d/"+docID+"/edit#gid=7" {
		t.Fatalf("canonical %s", got)
	}
}

func TestQuoteSheet(t *testing.T) {
	if got := sheets.QuoteSheet("It's here"); got != "'It''s here'" {
		t.Fatal(got)
	}
}

func TestParseServiceAccount(t *testing.T) {
	acct, err := sheets.ParseServiceAccount(sheetstest.Key())
	if err != nil || acct.Email != sheetstest.Email {
		t.Fatalf("parse: %v %+v", err, acct)
	}
	bad := []string{"", "{", `{"type":"user"}`,
		`{"type":"service_account","client_email":"x@y.z","private_key":"nope"}`,
		`{"type":"service_account","client_email":"not an email","private_key":""}`}
	for _, b := range bad {
		if _, err := sheets.ParseServiceAccount([]byte(b)); err == nil {
			t.Fatalf("%q accepted", b)
		}
	}
	if _, err := sheets.ParseServiceAccount([]byte(strings.Repeat(" ", sheets.MaxKeyBytes+1))); err == nil {
		t.Fatal("oversized key accepted")
	}
}

func newClient(t *testing.T, f *sheetstest.Fake) *sheets.Client {
	t.Helper()
	acct, err := sheets.ParseServiceAccount(sheetstest.Key())
	if err != nil {
		t.Fatal(err)
	}
	return sheets.New(acct, f, nil)
}

func TestClientReadWrite(t *testing.T) {
	f := &sheetstest.Fake{}
	f.Add(docID, &sheetstest.Tab{ID: 0, Title: "First"}, &sheetstest.Tab{ID: 42, Title: "It's", Rows: [][]string{{"a", "b"}, {"1", "2"}}})
	c := newClient(t, f)
	ctx := context.Background()
	gid := int64(42)
	sh, err := c.Resolve(ctx, sheets.Ref{SpreadsheetID: docID, GID: &gid})
	if err != nil || sh.Title != "It's" {
		t.Fatalf("resolve: %v %+v", err, sh)
	}
	first, err := c.Resolve(ctx, sheets.Ref{SpreadsheetID: docID})
	if err != nil || first.ID != 0 {
		t.Fatalf("first: %v %+v", err, first)
	}
	missing := int64(5)
	if _, err := c.Resolve(ctx, sheets.Ref{SpreadsheetID: docID, GID: &missing}); !sheets.IsAccess(err) {
		t.Fatalf("missing sheet: %v", err)
	}
	rows, err := c.Values(ctx, docID, sheets.QuoteSheet(sh.Title))
	if err != nil || !reflect.DeepEqual(rows, [][]string{{"a", "b"}, {"1", "2"}}) {
		t.Fatalf("values: %v %v", err, rows)
	}
	if err := c.Update(ctx, docID, sheets.QuoteSheet(sh.Title)+"!1:1", [][]string{{"a", "b", "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Append(ctx, docID, sheets.QuoteSheet(sh.Title), [][]string{{"=1+1", "x", "y"}}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a", "b", "c"}, {"1", "2"}, {"=1+1", "x", "y"}}
	if got := f.Rows(docID, 42); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v", got)
	}
	tokens := 0
	for _, call := range f.Calls() {
		if strings.Contains(call, "oauth2") {
			tokens++
		}
	}
	if tokens != 1 {
		t.Fatalf("token requested %d times, want 1 (cached)", tokens)
	}
}

func TestClientErrors(t *testing.T) {
	f := &sheetstest.Fake{}
	f.Add(docID, &sheetstest.Tab{ID: 0, Title: "S"})
	f.Deny(docID, true)
	c := newClient(t, f)
	ctx := context.Background()
	_, err := c.Sheets(ctx, docID)
	if !sheets.IsAccess(err) {
		t.Fatalf("denied: %v", err)
	}
	if strings.Contains(err.Error(), "permission") {
		t.Fatalf("Google text leaked into the message: %v", err)
	}
	f.Deny(docID, false)
	f.Status = http.StatusServiceUnavailable
	if _, err := c.Sheets(ctx, docID); err == nil || sheets.IsAccess(err) {
		t.Fatalf("unavailable: %v", err)
	}
	bad := &sheetstest.Fake{TokenStatus: http.StatusBadRequest}
	if _, err := newClient(t, bad).Token(ctx); !sheets.IsAuth(err) {
		t.Fatalf("revoked key: %v", err)
	}
}

// hostRecorder answers everything; the guard must stop requests before it.
type hostRecorder struct{ hits int }

func (h *hostRecorder) RoundTrip(*http.Request) (*http.Response, error) {
	h.hits++
	return nil, errors.New("must not be reached")
}

func TestGuardRefusesOtherHosts(t *testing.T) {
	rec := &hostRecorder{}
	acct, _ := sheets.ParseServiceAccount(sheetstest.Key())
	c := sheets.New(acct, rec, func() time.Time { return time.Unix(1_700_000_000, 0) })
	// The key's token_uri points to evil.example: it must be ignored, so
	// the token request goes to Google and reaches the transport.
	_, _ = c.Token(context.Background())
	if rec.hits != 1 {
		t.Fatalf("token request did not go to the fixed endpoint: hits %d", rec.hits)
	}
}
