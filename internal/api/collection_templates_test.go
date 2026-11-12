package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestCollectionTemplatesAPI(t *testing.T) {
	f := newFormHarness(t)
	wantStatus(t, f.do(http.MethodGet, "/api/v1/collection-templates", "", nil), http.StatusUnauthorized)
	rec := f.do(http.MethodGet, "/api/v1/collection-templates", f.owner, nil)
	wantStatus(t, rec, http.StatusOK)
	list := decodeAs[struct {
		Items []collectionTemplateDTO `json:"items"`
		Total int                     `json:"total"`
	}](t, rec)
	if list.Total != 7 || len(list.Items) != 7 || list.Items[0].Name != "menu" {
		t.Fatalf("templates %+v", list)
	}
	for _, tpl := range list.Items {
		if len(tpl.Samples) == 0 || !strings.Contains(tpl.Snippet, "const NAME = '"+tpl.Name+"'") || len(tpl.Schema) == 0 {
			t.Fatalf("template %s incomplete", tpl.Name)
		}
	}

	base := "/api/v1/sites/" + f.site.ID + "/collections"
	rec = f.do(http.MethodPost, base, f.owner, map[string]any{"name": "reviews", "template": "reviews", "with_samples": true})
	wantStatus(t, rec, http.StatusCreated)
	c := decodeAs[collectionDTO](t, rec)
	if c.Mode != "moderated" || c.Documents != 2 || !strings.Contains(string(c.Schema), `"rating"`) {
		t.Fatalf("created %+v", c)
	}
	wantStatus(t, f.visitor(http.MethodGet, "/_dropsite/data/reviews", "", ""), http.StatusOK)
	wantError(t, f.do(http.MethodPost, base, f.owner, map[string]any{"name": "blog", "template": "blog"}),
		http.StatusBadRequest, "collection_invalid")
}
