package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestPublicPageIsCutTo200(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "items"}); err != nil {
		t.Fatal(err)
	}
	docs := make([]DocumentInput, 0, 300)
	for i := range 300 {
		docs = append(docs, doc(fmt.Sprintf("d%03d", i), `{"n":1}`))
	}
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "items", docs, false); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", Limit: MaxDocumentPage})
	if err != nil || len(p.Items) != MaxPublicDocumentPage || p.Next == "" {
		t.Fatalf("visitor page: %d items, next %q, %v", len(p.Items), p.Next, err)
	}
	rest, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", Limit: MaxDocumentPage, After: p.Next})
	if err != nil || len(rest.Items) != 100 || rest.Next != "" {
		t.Fatalf("second page: %d %q %v", len(rest.Items), rest.Next, err)
	}
	// The owner API keeps its limit.
	own, err := e.svc.ListDocuments(ctx, ann, v.Site.ID, "items", DocumentQuery{Limit: MaxDocumentPage})
	if err != nil || len(own.Items) != 300 {
		t.Fatalf("owner page: %d %v", len(own.Items), err)
	}
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", Limit: MaxDocumentPage + 1})
	wantCode(t, err, domain.CodeInvalidInput)
}

func TestDataReadRateLimit(t *testing.T) {
	e := newEnv(t)
	for i := range DataReadsPerIP {
		if err := e.svc.AllowDataRead("198.51.100.1"); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	err := e.svc.AllowDataRead("198.51.100.1")
	wantCode(t, err, domain.CodeRateLimited)
	if domain.AsError(err).RetryAfter <= 0 {
		t.Fatal("no Retry-After")
	}
	// Another address and the same /64 network.
	if err := e.svc.AllowDataRead("198.51.100.2"); err != nil {
		t.Fatal(err)
	}
	for range DataReadsPerIP {
		_ = e.svc.AllowDataRead("2001:db8::1")
	}
	wantCode(t, e.svc.AllowDataRead("2001:db8::2"), domain.CodeRateLimited)
	e.clock.Advance(DataReadsPerIPWindow)
	if err := e.svc.AllowDataRead("198.51.100.1"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func dataVersion(t *testing.T, e *env, host, name string) int64 {
	t.Helper()
	p, err := e.svc.ReadCollection(context.Background(), DataRead{Host: host, Collection: name})
	if err != nil {
		t.Fatal(err)
	}
	return p.Version
}

func TestDataVersionChangesOnlyWithPublicData(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "reviews",
		Mode: domain.CollectionModerated, Schema: json.RawMessage(reviewSchema)}); err != nil {
		t.Fatal(err)
	}
	ver := dataVersion(t, e, host, "reviews")
	step := func(what string, wantBump bool) {
		t.Helper()
		got := dataVersion(t, e, host, "reviews")
		if (got != ver) != wantBump {
			t.Fatalf("%s: version %d -> %d, want bump %v", what, ver, got, wantBump)
		}
		ver = got
	}
	for range 3 {
		if _, err := e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"A","text":"x"}`)); err != nil {
			t.Fatal(err)
		}
	}
	step("visitor proposals", false)
	pending, err := e.svc.ListDocuments(ctx, ann, v.Site.ID, "reviews", DocumentQuery{Status: "pending"})
	if err != nil || len(pending.Items) != 3 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	ids := []string{pending.Items[0].ID, pending.Items[1].ID, pending.Items[2].ID}
	if _, _, err := e.svc.ModerateDocuments(ctx, ann, v.Site.ID, "reviews", ids[:1], false); err != nil {
		t.Fatal(err)
	}
	step("reject", false)
	if _, _, err := e.svc.DeleteDocuments(ctx, ann, v.Site.ID, "reviews", ids[1:2]); err != nil {
		t.Fatal(err)
	}
	step("delete a pending document", false)
	if _, _, err := e.svc.ModerateDocuments(ctx, ann, v.Site.ID, "reviews", ids[2:], true); err != nil {
		t.Fatal(err)
	}
	step("approve", true)
	if _, _, err := e.svc.DeleteDocuments(ctx, ann, v.Site.ID, "reviews", ids[2:]); err != nil {
		t.Fatal(err)
	}
	step("delete an approved document", true)
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "reviews", []DocumentInput{doc("own", `{"name":"B","text":"y"}`)}, false); err != nil {
		t.Fatal(err)
	}
	step("owner write", true)
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "reviews", nil, true); err != nil {
		t.Fatal(err)
	}
	step("replace", true)
}

func TestVisitorDocumentIsStoredUnescapedWithinLimit(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	schema := `{"type":"object","properties":{"name":{"type":"string"},"text":{"type":"string"}},"required":["name","text"]}`
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "reviews",
		Mode: domain.CollectionModerated, Schema: json.RawMessage(schema)}); err != nil {
		t.Fatal(err)
	}
	// Almost 8 KB of "<" would be ~48 KB with HTML escaping.
	text := strings.Repeat("<", MaxVisitorDocumentBytes-40)
	body := `{"name":"A&B","text":"` + text + `"}`
	if _, err := e.svc.SubmitDocument(ctx, reviewPost(host, body)); err != nil {
		t.Fatalf("submit: %v", err)
	}
	p, err := e.svc.ListDocuments(ctx, ann, v.Site.ID, "reviews", DocumentQuery{Status: "pending"})
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("pending: %v", err)
	}
	stored := string(p.Items[0].Data)
	if strings.Contains(stored, "\\u00") || !strings.Contains(stored, "A&B") || len(stored) > MaxVisitorDocumentBytes {
		t.Fatalf("stored escaped: %.80s ... %s", stored, stored[len(stored)-40:])
	}

	// Invalid UTF-8 grows to U+FFFD (1 byte -> 3): past 16 KB it is refused.
	raw := []byte(`{"name":"A","text":"` + strings.Repeat("\xff", MaxVisitorDocumentBytes-40) + `"}`)
	_, err = e.svc.SubmitDocument(ctx, reviewPost(host, string(raw)))
	wantCode(t, err, domain.CodeDocumentInvalid)
	if !strings.Contains(err.Error(), "16 KB") {
		t.Fatalf("error %v", err)
	}
}
