package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestHostnameEndpoints(t *testing.T) {
	var seen []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.EscapedPath())
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"items": []any{
				map[string]any{"hostname": "a.sites.test", "kind": "default", "verified": true, "status": "active"},
				map[string]any{"hostname": "promo.example.ru", "kind": "external", "status": "rejected", "reason": "not ours"},
			}, "total": 2})
		case http.MethodPost:
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			writeJSON(w, 201, map[string]any{"hostname": in["hostname"], "kind": "external", "status": "pending"})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	ctx := context.Background()
	list, err := c.ListHostnames(ctx, "promo")
	must(t, err)
	if len(list.Items) != 2 || list.Items[1].Reason == nil || *list.Items[1].Reason != "not ours" {
		t.Fatalf("list %+v", list)
	}
	h, err := c.AddHostname(ctx, "promo", "promo.example.ru")
	must(t, err)
	if h.Hostname != "promo.example.ru" || h.Status != "pending" {
		t.Fatalf("added %+v", h)
	}
	must(t, c.RemoveHostname(ctx, "promo", "a/b.example.ru"))
	want := []string{
		"GET /api/v1/sites/promo/hostnames",
		"POST /api/v1/sites/promo/hostnames",
		"DELETE /api/v1/sites/promo/hostnames/a%2Fb.example.ru",
	}
	if len(seen) != 3 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] {
		t.Fatalf("requests %v", seen)
	}
}
