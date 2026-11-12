package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/b0balYson/dropsite/internal/datatpl"
	"github.com/b0balYson/dropsite/internal/domain"
)

// Every template is created with its samples, which pass its schema and
// are public at once (approved, like any owner write).
func TestCreateEveryTemplateWithSamples(t *testing.T) {
	e, ann, v, host := collEnv(t)
	ctx := context.Background()
	for _, tpl := range e.svc.CollectionTemplates() {
		c, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: tpl.Name, Template: tpl.Name, WithSamples: true})
		if err != nil {
			t.Fatalf("%s: %v", tpl.Name, err)
		}
		if string(c.Mode) != tpl.Mode || c.Title != tpl.Title || c.Documents != len(tpl.Samples) {
			t.Fatalf("%s: %+v", tpl.Name, c.Collection)
		}
		if got, ok := datatpl.ForSchema(c.Schema); !ok || got.Name != tpl.Name {
			t.Fatalf("%s: stored schema not recognized: %s", tpl.Name, c.Schema)
		}
		p, err := e.svc.ReadCollection(ctx, DataRead{Host: host, Collection: tpl.Name})
		if err != nil || len(p.Items) != len(tpl.Samples) {
			t.Fatalf("%s: public read %d %v", tpl.Name, len(p.Items), err)
		}
	}
}

func TestTemplateDefaultsAndErrors(t *testing.T) {
	e, ann, v, _ := collEnv(t)
	ctx := context.Background()
	// Explicit values win over the template.
	own := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	c, err := e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "drinks", Title: "Drinks", Template: "MENU",
		Schema: own})
	if err != nil || c.Title != "Drinks" || c.Mode != domain.CollectionPublic || c.Documents != 0 {
		t.Fatalf("create: %+v %v", c, err)
	}
	if _, ok := datatpl.ForSchema(c.Schema); ok {
		t.Fatal("own schema replaced by the template")
	}
	// The reviews template is moderated, but the mode can be overridden.
	c, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "quotes", Template: "reviews",
		Mode: domain.CollectionPublic})
	if err != nil || c.Mode != domain.CollectionPublic {
		t.Fatalf("mode override: %+v %v", c, err)
	}
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "blog", Template: "blog"})
	wantCode(t, err, domain.CodeCollectionInvalid)
	if f := domain.AsError(err).Params["field"]; f != "template" {
		t.Fatalf("field %v", f)
	}
	_, err = e.svc.CreateCollection(ctx, ann, v.Site.ID, CollectionInput{Name: "x", WithSamples: true})
	wantCode(t, err, domain.CodeCollectionInvalid)
	if _, err := e.svc.GetCollection(ctx, ann, v.Site.ID, "blog"); err == nil {
		t.Fatal("collection created with an unknown template")
	}
	// Another user's site stays not found, template or not.
	bob := e.user("bob@pp.test")
	_, err = e.svc.CreateCollection(ctx, bob, v.Site.ID, CollectionInput{Name: "menu", Template: "menu"})
	if domain.AsError(err).Kind != domain.KindNotFound {
		t.Fatalf("foreign site: %v", err)
	}
}
