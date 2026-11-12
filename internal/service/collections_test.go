package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

const reviewSchema = `{"type":"object","properties":{"name":{"type":"string","maxLength":40},` +
	`"text":{"type":"string","minLength":1}},"required":["name","text"]}`

func collEnv(t *testing.T) (*env, Actor, SiteView, string) {
	t.Helper()
	e := newEnv(t)
	ann := e.user("ann@pp.test")
	v, err := e.svc.CreateSite(context.Background(), ann, "Cafe", "")
	if err != nil {
		t.Fatal(err)
	}
	return e, ann, v, v.Site.ID + "." + testBase
}

func doc(id, data string) DocumentInput { return DocumentInput{ID: id, Data: json.RawMessage(data)} }

func TestCreateCollectionRules(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	c, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "Menu", Mode: domain.CollectionPublic})
	if err != nil || c.Name != "menu" || !strings.HasSuffix(c.PublicURL, "/_dropsite/data/menu") {
		t.Fatalf("create: %+v %v", c, err)
	}
	p, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "menu"})
	if err != nil || len(p.Items) != 0 || p.Next != "" {
		t.Fatalf("empty read: %+v %v", p, err)
	}
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "reviews", Mode: domain.CollectionModerated})
	wantCode(t, err, domain.CodeCollectionInvalid)
	for _, name := range []string{"../x", "Меню", "", "-a", strings.Repeat("a", 65)} {
		_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: name})
		wantCode(t, err, domain.CodeCollectionInvalid)
	}
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu"})
	wantCode(t, err, domain.CodeCollectionExists)
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "x", Schema: json.RawMessage(`{"type":"array"}`)})
	wantCode(t, err, domain.CodeCollectionInvalid)
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "x",
		Schema: json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"https://example.com/s"}}}`)})
	wantCode(t, err, domain.CodeCollectionInvalid)
	for i := 1; i < MaxCollectionsPerSite; i++ {
		if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: fmt.Sprintf("c%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "extra"})
	wantCode(t, err, domain.CodeLimitExceeded)

	// Another user's site does not exist; neither does a collection through MCP for an admin.
	bob := e.user("bob@pp.test")
	_, _, err = e.svc.ListCollections(ctx, bob, v.Site.ID)
	wantNotFound(t, err)
	_, err = e.svc.SaveDocuments(ctx, bob, v.Site.ID, "menu", []DocumentInput{doc("a", `{}`)}, false)
	wantNotFound(t, err)
	if _, _, err := e.svc.ListCollections(ctx, e.admin, v.Site.ID); err != nil {
		t.Fatalf("admin: %v", err)
	}
	mcpAdmin := e.admin
	mcpAdmin.ViaMCP = true
	_, _, err = e.svc.ListCollections(ctx, mcpAdmin, v.Site.ID)
	wantNotFound(t, err)
}

func TestSaveDocumentsAndRead(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu",
		Schema: json.RawMessage(`{"type":"object","properties":{"price":{"type":"number"}}}`)}); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{
		doc("cappuccino", `{"price": 200}`), {Data: json.RawMessage(`{"price":150}`)},
	}, false)
	if err != nil || len(res.IDs) != 2 || res.IDs[0] != "cappuccino" || res.IDs[1] == "" || res.Collection.Documents != 2 {
		t.Fatalf("save: %+v %v", res, err)
	}
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("cappuccino", `{"price":250}`)}, false); err != nil {
		t.Fatal(err)
	}
	d, _, err := e.svc.ReadDocument(ctx, host, "menu", "cappuccino")
	if err != nil || string(d.Data) != `{"price":250}` {
		t.Fatalf("read: %s %v", d.Data, err)
	}

	// One invalid document fails the whole write and names it.
	batch := make([]DocumentInput, 10)
	for i := range batch {
		batch[i] = doc(fmt.Sprintf("d%d", i), `{"price":1}`)
	}
	batch[7] = doc("d7", `{"price":"cheap"}`)
	_, err = e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", batch, false)
	wantCode(t, err, domain.CodeDocumentInvalid)
	if de := domain.AsError(err); de.Params["index"] != 7 || de.Params["field"] != "price" {
		t.Fatalf("params: %v", de.Params)
	}
	if _, _, err := e.svc.ReadDocument(ctx, host, "menu", "d0"); err == nil {
		t.Fatal("partial write")
	}
	big := `{"price":1,"note":"` + strings.Repeat("x", 20<<10) + `"}`
	_, err = e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("big", big)}, false)
	wantCode(t, err, domain.CodeDocumentInvalid)
	_, err = e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("arr", `[1]`)}, false)
	wantCode(t, err, domain.CodeDocumentInvalid)
	_, err = e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("Bad id!", `{}`)}, false)
	wantCode(t, err, domain.CodeDocumentInvalid)

	// Replace leaves exactly the new set.
	var thirty []DocumentInput
	for i := range 30 {
		thirty = append(thirty, doc(fmt.Sprintf("p%02d", i), fmt.Sprintf(`{"price":%d}`, i)))
	}
	if res, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", thirty, true); err != nil || res.Collection.Documents != 30 {
		t.Fatalf("replace: %+v %v", res.Collection, err)
	}

	// Data is not part of releases: a rollback does not touch it.
	if _, err := e.svc.Deploy(ctx, ann, v.Site.ID, zipOf(t, map[string]string{"index.html": "a"}), domain.SourceAPI); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Second)
	if _, err := e.svc.Deploy(ctx, ann, v.Site.ID, zipOf(t, map[string]string{"index.html": "b"}), domain.SourceAPI); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rollback(ctx, ann, v.Site.ID, ""); err != nil {
		t.Fatal(err)
	}
	if d, _, err := e.svc.ReadDocument(ctx, host, "menu", "p05"); err != nil || string(d.Data) != `{"price":5}` {
		t.Fatalf("after rollback: %s %v", d.Data, err)
	}
}

func TestPublicReadPaginationAndHosts(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "items"}); err != nil {
		t.Fatal(err)
	}
	var docs []DocumentInput
	for i := range 250 {
		p := int64(i % 7)
		docs = append(docs, DocumentInput{ID: fmt.Sprintf("i%03d", i), Data: json.RawMessage(`{}`), Position: &p})
	}
	for i := 0; i < 250; i += 125 {
		if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "items", docs[i:i+125], false); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	after, pages := "", 0
	for {
		p, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", Limit: 100, After: after})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for i, d := range p.Items {
			if seen[d.ID] {
				t.Fatalf("repeated %s", d.ID)
			}
			if i > 0 && p.Items[i-1].Position > d.Position {
				t.Fatal("not ordered by position")
			}
			seen[d.ID] = true
		}
		if p.Next == "" {
			break
		}
		after = p.Next
	}
	if len(seen) != 250 || pages != 3 {
		t.Fatalf("seen %d in %d pages", len(seen), pages)
	}
	_, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", Limit: 501})
	wantCode(t, err, domain.CodeInvalidInput)
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", After: "garbage"})
	wantCode(t, err, domain.CodeInvalidInput)
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "items", Order: "newest", After: after})
	if after != "" {
		wantCode(t, err, domain.CodeInvalidInput)
	}

	// Another site's host does not see the collection.
	other, err := e.svc.CreateSite(ctx, ann, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: other.Site.ID + "." + testBase, Collection: "items"})
	wantNotFound(t, err)
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: "nope." + testBase, Collection: "items"})
	wantNotFound(t, err)
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "ITEMS"})
	wantNotFound(t, err)
}

func reviewPost(host, body string) DataPost {
	return DataPost{Host: host, Collection: "reviews", IP: "198.51.100.9", Body: []byte(body)}
}

func TestVisitorProposalsAndModeration(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "reviews",
		Mode: domain.CollectionModerated, Schema: json.RawMessage(reviewSchema)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu"}); err != nil {
		t.Fatal(err)
	}
	stored, err := e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"Анна","text":"Вкусно","_t":"1","_hp":""}`))
	if err != nil || !stored {
		t.Fatalf("submit: %v %v", stored, err)
	}
	if p, _ := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "reviews"}); len(p.Items) != 0 {
		t.Fatal("pending review is public")
	}
	_, err = e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"A","text":"x","is_admin":true}`))
	wantCode(t, err, domain.CodeDocumentInvalid)
	_, err = e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"A"}`))
	wantCode(t, err, domain.CodeDocumentInvalid)
	_, err = e.svc.SubmitDocument(ctx, DataPost{Host: host, Collection: "menu", Body: []byte(`{}`)})
	wantCode(t, err, domain.CodeCollectionReadOnly)
	p := reviewPost(host, `{"name":"A","text":"x"}`)
	p.Origin = "https://evil.example"
	_, err = e.svc.SubmitDocument(ctx, p)
	wantCode(t, err, domain.CodeFormOrigin)
	if stored, err := e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"Bot","text":"spam","_hp":"x"}`)); err != nil || stored {
		t.Fatalf("honeypot: %v %v", stored, err)
	}
	_, err = e.svc.SubmitDocument(ctx, reviewPost(host, `[1]`))
	wantCode(t, err, domain.CodeDocumentInvalid)

	// Altcha by the site flag.
	if _, err := e.svc.SetFormSiteSettings(ctx, ann, v.Site.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"A","text":"x"}`))
	wantCode(t, err, domain.CodeFormCaptcha)
	e.minAltcha()
	ch, err := e.svc.AltchaChallenge(ctx, host, "198.51.100.20")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"name": "B", "text": "ok", "altcha": solve(t, ch)})
	solved := reviewPost(host, string(body))
	solved.IP = "198.51.100.20"
	if stored, err := e.svc.SubmitDocument(ctx, solved); err != nil || !stored {
		t.Fatalf("solved: %v %v", stored, err)
	}
	if _, err := e.svc.SetFormSiteSettings(ctx, ann, v.Site.ID, false); err != nil {
		t.Fatal(err)
	}

	page, err := e.svc.ListDocuments(ctx, ann, v.Site.ID, "reviews", DocumentQuery{Status: "pending"})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("pending: %+v %v", page, err)
	}
	for _, d := range page.Items {
		if strings.Contains(string(d.Data), "altcha") || strings.Contains(string(d.Data), "_t") {
			t.Fatalf("platform fields stored: %s", d.Data)
		}
	}
	n, c, err := e.svc.ModerateDocuments(ctx, ann, v.Site.ID, "reviews", []string{page.Items[0].ID}, true)
	if err != nil || n != 1 || c.Pending != 1 {
		t.Fatalf("approve: %d %+v %v", n, c, err)
	}
	if p, _ := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "reviews"}); len(p.Items) != 1 {
		t.Fatal("approved review not public")
	}
	n, c, err = e.svc.ModerateDocuments(ctx, ann, v.Site.ID, "reviews", []string{page.Items[1].ID}, false)
	if err != nil || n != 1 || c.Documents != 1 {
		t.Fatalf("reject: %d %+v %v", n, c, err)
	}
	if _, err := e.svc.GetDocument(ctx, ann, v.Site.ID, "reviews", page.Items[1].ID); err == nil {
		t.Fatal("rejected document kept")
	}
	rows, _, err := e.repo.ListAudit(ctx, store.AuditFilter{Action: "collection.moderate", Page: store.Page{Limit: 100}})
	if err != nil || len(rows) != 2 {
		t.Fatalf("audit: %d %v", len(rows), err)
	}
}

func TestVisitorRateLimitAndPendingLimit(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "reviews",
		Mode: domain.CollectionModerated, Schema: json.RawMessage(reviewSchema)}); err != nil {
		t.Fatal(err)
	}
	var err error
	for i := 0; i <= FormPostsPerIP; i++ {
		_, err = e.svc.SubmitDocument(ctx, reviewPost(host, `{"name":"A","text":"x"}`))
	}
	wantCode(t, err, domain.CodeRateLimited)
	// Forms keep their own quota.
	if _, err := e.svc.SubmitForm(ctx, FormPost{Host: host, Form: "lead", IP: "198.51.100.9"}); err != nil {
		t.Fatalf("form after data limit: %v", err)
	}
}

func TestDeleteCollectionNeedsConfirm(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu"}); err != nil {
		t.Fatal(err)
	}
	err := e.svc.DeleteCollection(ctx, ann, v.Site.ID, "menu", "men")
	wantCode(t, err, domain.CodeInvalidInput)
	if err := e.svc.DeleteCollection(ctx, ann, v.Site.ID, "menu", "MENU"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "menu"})
	wantNotFound(t, err)
}

func TestSchemaChangeAppliesToNewDocuments(t *testing.T) {
	e, ann, v, _ := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("a", `{"price":"x"}`)}, false); err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"price":{"type":"number"}}}`)
	if _, err := e.svc.UpdateCollection(ctx, ann, v.Site.ID, "menu", CollectionUpdate{Schema: &schema}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("b", `{"price":"y"}`)}, false)
	wantCode(t, err, domain.CodeDocumentInvalid)
	mode := domain.CollectionModerated
	none := json.RawMessage("null")
	_, err = e.svc.UpdateCollection(ctx, ann, v.Site.ID, "menu", CollectionUpdate{Mode: &mode, Schema: &none})
	wantCode(t, err, domain.CodeCollectionInvalid)
	c, err := e.svc.UpdateCollection(ctx, ann, v.Site.ID, "menu", CollectionUpdate{Schema: &none})
	if err != nil || c.Schema != nil {
		t.Fatalf("remove schema: %+v %v", c, err)
	}
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", []DocumentInput{doc("b", `{"price":"y"}`)}, false); err != nil {
		t.Fatalf("schema still applied: %v", err)
	}
}

func TestDeleteSiteRemovesCollections(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "menu"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteSite(ctx, ann, v.Site.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: "menu"})
	wantNotFound(t, err)
	if cs, err := e.repo.ListCollections(ctx, v.Site.ID); err != nil || len(cs) != 0 {
		t.Fatalf("left %v %v", cs, err)
	}
}
