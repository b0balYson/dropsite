package api

import (
	"net/http"
	"testing"
)

func TestWorkspacesAPI(t *testing.T) {
	h := newHarness(t)
	_, ann := h.user("ann@pp.test")
	bobActor, bob := h.user("bob@pp.test")
	_, eve := h.user("eve@pp.test")

	rec := h.do(http.MethodPost, "/api/v1/workspaces", ann, map[string]string{"name": "Marketing"})
	wantStatus(t, rec, http.StatusCreated)
	ws := decodeAs[workspaceDTO](t, rec)
	if ws.Name != "Marketing" || len(ws.Members) != 1 || ws.Members[0].Email != "ann@pp.test" {
		t.Fatalf("created %+v", ws)
	}
	base := "/api/v1/workspaces/" + ws.ID
	wantError(t, h.do(http.MethodPost, "/api/v1/workspaces", ann, map[string]string{"name": ""}),
		http.StatusBadRequest, "workspace_name_invalid")
	wantError(t, h.do(http.MethodPost, base+"/members", ann, map[string]string{"email": "nobody@pp.test"}),
		http.StatusBadRequest, "workspace_user_not_found")
	rec = h.do(http.MethodPost, base+"/members", ann, map[string]string{"email": "bob@pp.test"})
	wantStatus(t, rec, http.StatusCreated)
	if got := decodeAs[workspaceDTO](t, rec); got.MemberCount != 2 {
		t.Fatalf("after add %+v", got)
	}
	wantError(t, h.do(http.MethodPost, base+"/members", bob, map[string]string{"email": "ann@pp.test"}),
		http.StatusConflict, "workspace_member_exists")

	// Strangers: 404 everywhere.
	wantStatus(t, h.do(http.MethodGet, base, eve, nil), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodPatch, base, eve, map[string]string{"name": "x"}), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodPost, base+"/members", eve, map[string]string{"email": "eve@pp.test"}), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodDelete, base, eve, nil), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodGet, "/api/v1/workspaces?all=true", eve, nil), http.StatusNotFound)
	l := decodeAs[list[workspaceDTO]](t, h.do(http.MethodGet, "/api/v1/workspaces", eve, nil))
	if l.Total != 0 {
		t.Fatalf("eve list %+v", l)
	}

	// A site created in the workspace is shared; the list shows and filters it.
	rec = h.do(http.MethodPost, "/api/v1/sites", ann, map[string]string{"title": "Shop", "workspace_id": ws.ID})
	wantStatus(t, rec, http.StatusCreated)
	shop := decodeAs[siteDTO](t, rec)
	if shop.WorkspaceID == nil || *shop.WorkspaceID != ws.ID || *shop.WorkspaceName != "Marketing" {
		t.Fatalf("site %+v", shop)
	}
	wantStatus(t, h.do(http.MethodPost, "/api/v1/sites", eve, map[string]string{"title": "x", "workspace_id": ws.ID}),
		http.StatusNotFound)
	wantStatus(t, h.do(http.MethodGet, "/api/v1/sites/"+shop.ID, eve, nil), http.StatusNotFound)
	personal := h.createSite(bob, "Bob's", "")
	items := decodeAs[list[siteItemDTO]](t, h.do(http.MethodGet, "/api/v1/sites", bob, nil))
	if items.Total != 2 {
		t.Fatalf("bob sites %+v", items)
	}
	items = decodeAs[list[siteItemDTO]](t, h.do(http.MethodGet, "/api/v1/sites?workspace=personal", bob, nil))
	if items.Total != 1 || items.Items[0].ID != personal.ID || items.Items[0].WorkspaceID != nil {
		t.Fatalf("bob personal %+v", items)
	}
	items = decodeAs[list[siteItemDTO]](t, h.do(http.MethodGet, "/api/v1/sites?workspace="+ws.ID, bob, nil))
	if items.Total != 1 || items.Items[0].ID != shop.ID || *items.Items[0].WorkspaceName != "Marketing" {
		t.Fatalf("bob workspace %+v", items)
	}
	wantStatus(t, h.do(http.MethodGet, "/api/v1/sites?workspace="+ws.ID, eve, nil), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodGet, "/api/v1/sites?workspace=bad", bob, nil), http.StatusBadRequest)

	// Move: only the creator; null brings it back.
	wantError(t, h.do(http.MethodPost, "/api/v1/sites/"+shop.ID+"/move", bob, map[string]any{"workspace_id": nil}),
		http.StatusConflict, "workspace_move_forbidden")
	rec = h.do(http.MethodPost, "/api/v1/sites/"+personal.ID+"/move", bob, map[string]any{"workspace_id": ws.ID})
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[siteDTO](t, rec); got.WorkspaceID == nil || got.URL != personal.URL {
		t.Fatalf("moved %+v", got)
	}
	wantStatus(t, h.do(http.MethodPost, "/api/v1/sites/"+personal.ID+"/move", bob, map[string]any{}), http.StatusBadRequest)

	// Leaving: the last member stays while sites remain.
	wantStatus(t, h.do(http.MethodDelete, base+"/members/"+idString(bobActor.User.ID), bob, nil), http.StatusNoContent)
	wantStatus(t, h.do(http.MethodGet, base, bob, nil), http.StatusNotFound)
	wantStatus(t, h.do(http.MethodGet, "/api/v1/sites/"+personal.ID, bob, nil), http.StatusNotFound)
	wantError(t, h.do(http.MethodDelete, base, ann, nil), http.StatusConflict, "workspace_not_empty")
	rec = h.do(http.MethodPatch, base, ann, map[string]string{"name": "Sales"})
	wantStatus(t, rec, http.StatusOK)
	if decodeAs[workspaceDTO](t, rec).Name != "Sales" {
		t.Fatal("rename")
	}
}
