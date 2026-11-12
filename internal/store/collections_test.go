package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func mkCollection(t *testing.T, db *DB, siteID, name string, mode domain.CollectionMode) {
	t.Helper()
	if err := db.CreateCollection(context.Background(), domain.Collection{
		SiteID: siteID, Name: name, Mode: mode, CreatedAt: t0, UpdatedAt: t0,
	}, 20); err != nil {
		t.Fatal(err)
	}
}

func pos(v int64) *int64 { return &v }

func TestCollectionsTablesExist(t *testing.T) {
	db := newDB(t)
	for _, table := range []string{"site_collections", "site_documents"} {
		var n int
		if err := db.Reader().QueryRowContext(context.Background(),
			"SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestCollectionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "s1", u, "", t0)
	mkCollection(t, db, "s1", "menu", domain.CollectionPublic)

	err := db.CreateCollection(ctx, domain.Collection{SiteID: "s1", Name: "menu", Mode: domain.CollectionPublic, CreatedAt: t0, UpdatedAt: t0}, 20)
	if !errors.Is(err, errCollectionExists) {
		t.Fatalf("duplicate: %v", err)
	}
	err = db.CreateCollection(ctx, domain.Collection{SiteID: "s1", Name: "x", Mode: domain.CollectionPublic, CreatedAt: t0, UpdatedAt: t0}, 1)
	if e := domain.AsError(err); e.Code != domain.CodeLimitExceeded {
		t.Fatalf("limit: %v", err)
	}

	w := DocumentsWrite{SiteID: "s1", Collection: "menu", MaxDocs: 3, Now: t0, Docs: []DocumentInput{
		{ID: "b", Data: json.RawMessage(`{"n":2}`), Position: pos(2)},
		{ID: "a", Data: json.RawMessage(`{"n":1}`), Position: pos(1)},
	}}
	if err := db.SaveDocuments(ctx, w); err != nil {
		t.Fatal(err)
	}
	// An update without a position keeps the stored one.
	if err := db.SaveDocuments(ctx, DocumentsWrite{SiteID: "s1", Collection: "menu", MaxDocs: 3, Now: t0,
		Docs: []DocumentInput{{ID: "b", Data: json.RawMessage(`{"n":20}`)}}}); err != nil {
		t.Fatal(err)
	}
	d, err := db.GetDocument(ctx, "s1", "menu", "b")
	if err != nil || d.Position != 2 || string(d.Data) != `{"n":20}` || d.Status != domain.DocumentApproved {
		t.Fatalf("doc: %+v %v", d, err)
	}
	// Exceeding the limit writes nothing.
	err = db.SaveDocuments(ctx, DocumentsWrite{SiteID: "s1", Collection: "menu", MaxDocs: 3, Now: t0, Docs: []DocumentInput{
		{ID: "c", Data: json.RawMessage(`{}`)}, {ID: "d", Data: json.RawMessage(`{}`)},
	}})
	if e := domain.AsError(err); e.Code != domain.CodeLimitExceeded {
		t.Fatalf("doc limit: %v", err)
	}
	if _, err := db.GetDocument(ctx, "s1", "menu", "c"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("partial write: %v", err)
	}
	docs, more, err := db.ListDocuments(ctx, DocumentFilter{SiteID: "s1", Collection: "menu", Order: OrderPosition, Limit: 10})
	if err != nil || more || len(docs) != 2 || docs[0].ID != "a" {
		t.Fatalf("list: %v %v %v", docs, more, err)
	}
	c, err := db.GetCollection(ctx, "s1", "menu")
	if err != nil || c.Documents != 2 || c.DataVersion != 2 {
		t.Fatalf("collection: %+v %v", c, err)
	}

	// Replace swaps the whole content.
	if err := db.SaveDocuments(ctx, DocumentsWrite{SiteID: "s1", Collection: "menu", MaxDocs: 3, Now: t0, Replace: true,
		Docs: []DocumentInput{{ID: "z", Data: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	if c, _ := db.GetCollection(ctx, "s1", "menu"); c.Documents != 1 {
		t.Fatalf("after replace: %+v", c)
	}
	if n, err := db.DeleteDocuments(ctx, "s1", "menu", []string{"z", "nope"}, t0); err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if _, err := db.DeleteDocuments(ctx, "s1", "nope", []string{"z"}, t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing collection: %v", err)
	}
	if err := db.DeleteCollection(ctx, "s1", "menu"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteCollection(ctx, "s1", "menu"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestPendingDocumentsAndModeration(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "s1", u, "", t0)
	mkCollection(t, db, "s1", "reviews", domain.CollectionModerated)
	for i := range 3 {
		err := db.InsertPendingDocument(ctx, domain.Document{SiteID: "s1", Collection: "reviews", ID: fmt.Sprintf("r%d", i),
			Data: json.RawMessage(`{}`), CreatedAt: t0}, 10, 2)
		if i < 2 && err != nil {
			t.Fatal(err)
		}
		if i == 2 && !errors.Is(err, domain.ErrCollectionPendingFull) {
			t.Fatalf("pending full: %v", err)
		}
	}
	approved, _, _ := db.ListDocuments(ctx, DocumentFilter{SiteID: "s1", Collection: "reviews", Status: domain.DocumentApproved, Limit: 10})
	if len(approved) != 0 {
		t.Fatalf("approved: %v", approved)
	}
	if n, err := db.ModerateDocuments(ctx, "s1", "reviews", []string{"r0"}, true, t0); err != nil || n != 1 {
		t.Fatalf("approve: %d %v", n, err)
	}
	if n, err := db.ModerateDocuments(ctx, "s1", "reviews", []string{"r0", "r1"}, false, t0); err != nil || n != 1 {
		t.Fatalf("reject: %d %v", n, err)
	}
	c, _ := db.GetCollection(ctx, "s1", "reviews")
	if c.Documents != 1 || c.Pending != 0 {
		t.Fatalf("counts: %+v", c)
	}
}

func TestDocumentPagination(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "s1", u, "", t0)
	mkCollection(t, db, "s1", "items", domain.CollectionPublic)
	var docs []DocumentInput
	for i := range 25 {
		docs = append(docs, DocumentInput{ID: fmt.Sprintf("d%02d", i), Data: json.RawMessage(`{}`), Position: pos(int64(i % 3))})
	}
	if err := db.SaveDocuments(ctx, DocumentsWrite{SiteID: "s1", Collection: "items", Docs: docs, MaxDocs: 100, Now: t0}); err != nil {
		t.Fatal(err)
	}
	for _, order := range []string{OrderPosition, OrderNewest, OrderOldest} {
		seen := map[string]bool{}
		var after *DocumentCursor
		for page := 0; ; page++ {
			got, more, err := db.ListDocuments(ctx, DocumentFilter{SiteID: "s1", Collection: "items", Order: order, After: after, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range got {
				if seen[d.ID] {
					t.Fatalf("%s: repeated %s", order, d.ID)
				}
				seen[d.ID] = true
			}
			if !more {
				break
			}
			last := got[len(got)-1]
			after = &DocumentCursor{Position: last.Position, CreatedAt: last.CreatedAt, ID: last.ID}
		}
		if len(seen) != 25 {
			t.Fatalf("%s: saw %d", order, len(seen))
		}
	}
}

func TestSoftDeleteSiteRemovesCollections(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	u := mkUser(t, db, "a@x.test", domain.UserActive)
	mkSite(t, db, "s1", u, "", t0)
	mkCollection(t, db, "s1", "menu", domain.CollectionPublic)
	if err := db.SaveDocuments(ctx, DocumentsWrite{SiteID: "s1", Collection: "menu", MaxDocs: 10, Now: t0,
		Docs: []DocumentInput{{ID: "a", Data: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SoftDeleteSite(ctx, "s1", t0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Reader().QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM site_collections) + (SELECT COUNT(*) FROM site_documents)").Scan(&n); err != nil || n != 0 {
		t.Fatalf("left %d rows: %v", n, err)
	}
}
