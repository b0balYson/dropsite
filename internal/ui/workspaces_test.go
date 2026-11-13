package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestWorkspacePages(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.env.User("bob@pp.test")
	alice := h.loginUser("alice@pp.test")

	p := h.get("/workspaces")
	wantStatus(t, p, http.StatusOK)
	wantContains(t, p.body, "Пространства", `action="/workspaces"`)
	p = h.submit("/workspaces", "/workspaces", url.Values{"name": {""}})
	wantStatus(t, p, http.StatusUnprocessableEntity)
	p = h.submit("/workspaces", "/workspaces", url.Values{"name": {"Маркетинг"}})
	wantStatus(t, p, http.StatusSeeOther)
	wsPath := p.location()
	p = h.follow(p)
	wantContains(t, p.body, "Маркетинг", "alice@pp.test", "Пространство создано")

	p = h.submit(wsPath, wsPath+"/members", url.Values{"email": {"nobody@pp.test"}})
	p = h.follow(p)
	wantContains(t, p.body, "нет активного пользователя")
	p = h.follow(h.submit(wsPath, wsPath+"/members", url.Values{"email": {"bob@pp.test"}}))
	wantContains(t, p.body, "bob@pp.test теперь участник")

	// "My sites": create in the workspace, filter, badge.
	list, err := h.env.Svc.ListWorkspaces(ctx, alice, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("workspaces %v %v", list, err)
	}
	ws := list[0]
	p = h.get("/")
	wantContains(t, p.body, `name="workspace"`, "/?workspace=personal", "Маркетинг")
	p = h.submit("/", "/sites", url.Values{"title": {"Общий"}, "workspace": {idOf(ws.ID)}})
	wantStatus(t, p, http.StatusSeeOther)
	sitePage := p.location()
	h.createSite(alice, "Блог", "")
	p = h.get("/?workspace=" + idOf(ws.ID))
	wantContains(t, p.body, "Общий", "Пространство: Маркетинг")
	wantNotContains(t, p.body, "Блог")
	p = h.get("/?workspace=personal")
	wantContains(t, p.body, "Блог")
	wantNotContains(t, p.body, ">Общий<")

	// Site page: workspace block, move needs confirmation.
	p = h.get(sitePage + "/settings")
	wantContains(t, p.body, "Сайт принадлежит пространству «Маркетинг»", "/move")
	p = h.follow(h.submit(sitePage, sitePage+"/move", url.Values{"workspace": {"0"}}))
	wantContains(t, p.body, "Сначала отметьте подтверждение")
	p = h.follow(h.submit(sitePage, sitePage+"/move", url.Values{"workspace": {"0"}, "confirm": {"yes"}}))
	wantContains(t, p.body, "Сайт снова личный")

	// Delete needs an empty workspace; leaving as the last member deletes it.
	p = h.follow(h.submit(wsPath, wsPath+"/members/"+idOf(alice.User.ID)+"/remove", url.Values{"confirm": {"yes"}}))
	wantContains(t, p.body, "Вы вышли из пространства")
	wantStatus(t, h.get(wsPath), http.StatusNotFound)
}

func TestWorkspacePageForbiddenToStrangers(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	bob := h.env.User("bob@pp.test")
	ws, err := h.env.Svc.CreateWorkspace(ctx, bob, "Team")
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.env.Svc.CreateSiteIn(ctx, bob, "Shop", "", ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.loginUser("eve@pp.test")
	wantStatus(t, h.get("/workspaces/"+idOf(ws.ID)), http.StatusNotFound)
	wantStatus(t, h.get("/sites/"+v.Site.ID), http.StatusNotFound)
	wantStatus(t, h.get("/?workspace="+idOf(ws.ID)), http.StatusNotFound)
	p := h.submit("/workspaces", "/workspaces/"+idOf(ws.ID)+"/members", url.Values{"email": {"eve@pp.test"}})
	wantStatus(t, p, http.StatusNotFound)
}
