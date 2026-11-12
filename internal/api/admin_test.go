package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestAdminUsers(t *testing.T) {
	h := newHarness(t)

	rec := h.do(http.MethodPost, "/api/v1/admin/users", h.adminToken, `{"email":"new@pp.test"}`)
	wantStatus(t, rec, http.StatusCreated)
	inv := decodeAs[inviteDTO](t, rec)
	if inv.User.Status != "invited" || inv.User.Role != "user" || !strings.Contains(inv.InviteURL, "/invite/") ||
		inv.InviteExpiresAt == nil || !inv.InviteExpiresAt.Equal(testenv.Start.Add(72*time.Hour)) {
		t.Fatalf("invite %+v", inv)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/admin/users", h.adminToken, `{"email":"new@pp.test"}`), http.StatusConflict, domain.CodeEmailTaken)
	wantError(t, h.do(http.MethodPost, "/api/v1/admin/users", h.adminToken, `{"email":"x@pp.test","role":"root"}`), http.StatusBadRequest, domain.CodeInvalidInput)
	rec = h.do(http.MethodPost, "/api/v1/admin/users", h.adminToken, `{"email":"boss@pp.test","role":"admin"}`)
	if decodeAs[inviteDTO](t, rec).User.Role != "admin" {
		t.Fatal("role ignored")
	}

	l := decodeAs[list[userItemDTO]](t, h.do(http.MethodGet, "/api/v1/admin/users?status=invited&q=new", h.adminToken, nil))
	if l.Total != 1 || l.Items[0].Email != "new@pp.test" {
		t.Fatalf("users %+v", l)
	}
	wantError(t, h.do(http.MethodGet, "/api/v1/admin/users?status=weird", h.adminToken, nil), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodGet, "/api/v1/admin/users?limit=0", h.adminToken, nil), http.StatusBadRequest, domain.CodeInvalidInput)

	path := "/api/v1/admin/users/" + inv.User.ID
	rec = h.do(http.MethodPatch, path, h.adminToken, `{"reissue_invite":true}`)
	wantStatus(t, rec, http.StatusOK)
	if re := decodeAs[inviteDTO](t, rec); re.InviteURL == "" || re.InviteURL == inv.InviteURL || re.InviteExpiresAt == nil {
		t.Fatalf("reissue %+v", re)
	}
	rec = h.do(http.MethodPatch, path, h.adminToken, `{"blocked":true}`)
	if !strings.Contains(rec.Body.String(), `"status":"blocked"`) || strings.Contains(rec.Body.String(), "invite_url") {
		t.Fatalf("block %s", rec.Body.String())
	}
	wantError(t, h.do(http.MethodPatch, path, h.adminToken, `{"reissue_invite":true}`), http.StatusBadRequest, domain.CodeUserBlocked)
	rec = h.do(http.MethodPatch, path, h.adminToken, `{"blocked":false}`)
	if !strings.Contains(rec.Body.String(), `"status":"invited"`) {
		t.Fatalf("unblock %s", rec.Body.String())
	}

	for _, body := range []string{`{}`, `{"blocked":true,"reissue_invite":true}`, `{"reissue_invite":false}`, `{"blocked":"yes"}`} {
		wantError(t, h.do(http.MethodPatch, path, h.adminToken, body), http.StatusBadRequest, domain.CodeInvalidInput)
	}
	self := "/api/v1/admin/users/" + idString(h.env.Admin.User.ID)
	wantError(t, h.do(http.MethodPatch, self, h.adminToken, `{"blocked":true}`), http.StatusBadRequest, domain.CodeSelfAction)
	wantError(t, h.do(http.MethodPatch, "/api/v1/admin/users/x", h.adminToken, `{"blocked":true}`), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodPatch, "/api/v1/admin/users/99999", h.adminToken, `{"blocked":true}`), http.StatusNotFound, domain.CodeNotFound)
}

func TestAdminSitesApexAndTransfer(t *testing.T) {
	h := newHarness(t)
	_, bob := h.user("bob@pp.test")
	carol, _ := h.user("carol@pp.test")
	site := h.createSite(bob, "Landing", "landing")

	l := decodeAs[list[siteItemDTO]](t, h.do(http.MethodGet, "/api/v1/admin/sites?q=landing", h.adminToken, nil))
	if l.Total != 1 || l.Items[0].ID != site.ID {
		t.Fatalf("admin sites %+v", l)
	}

	rec := h.do(http.MethodPost, "/api/v1/admin/sites/landing/apex", h.adminToken, nil)
	wantStatus(t, rec, http.StatusOK)
	s := decodeAs[siteDTO](t, rec)
	if !hasHost(s, "pp.test", "apex") || s.URLs[len(s.URLs)-1] != "https://pp.test/" {
		t.Fatalf("apex %+v", s)
	}
	rec = h.do(http.MethodDelete, "/api/v1/admin/sites/"+site.ID+"/apex", h.adminToken, nil)
	wantStatus(t, rec, http.StatusOK)
	if hasHost(decodeAs[siteDTO](t, rec), "pp.test", "apex") {
		t.Fatal("apex not cleared")
	}

	rec = h.do(http.MethodPost, "/api/v1/admin/sites/"+site.ID+"/transfer", h.adminToken, map[string]string{"user_id": idString(carol.User.ID)})
	wantStatus(t, rec, http.StatusOK)
	if decodeAs[siteDTO](t, rec).OwnerID != idString(carol.User.ID) {
		t.Fatal("not transferred")
	}
	// Numeric user_id is accepted too.
	rec = h.do(http.MethodPost, "/api/v1/admin/sites/"+site.ID+"/transfer", h.adminToken, `{"user_id":`+idString(h.env.Admin.User.ID)+`}`)
	wantStatus(t, rec, http.StatusOK)
	wantError(t, h.do(http.MethodPost, "/api/v1/admin/sites/"+site.ID+"/transfer", h.adminToken, `{"user_id":"abc"}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodPost, "/api/v1/admin/sites/"+site.ID+"/transfer", h.adminToken, `{"user_id":"99999"}`), http.StatusBadRequest, domain.CodeInvalidInput)
	wantError(t, h.do(http.MethodPost, "/api/v1/admin/sites/nope/transfer", h.adminToken, `{"user_id":"1"}`), http.StatusNotFound, domain.CodeNotFound)
}

func hasHost(s siteDTO, host, kind string) bool {
	for _, hn := range s.Hostnames {
		if hn.Hostname == host && hn.Kind == kind {
			return true
		}
	}
	return false
}

func TestAdminAudit(t *testing.T) {
	h := newHarness(t)
	bob, tok := h.user("bob@pp.test")
	h.env.Clock.Advance(24 * time.Hour) // 2026-11-13
	h.createSite(tok, "Audited", "")

	get := func(q string) list[auditDTO] {
		t.Helper()
		rec := h.do(http.MethodGet, "/api/v1/admin/audit?"+q, h.adminToken, nil)
		wantStatus(t, rec, http.StatusOK)
		return decodeAs[list[auditDTO]](t, rec)
	}
	all := get("")
	if all.Total < 4 {
		t.Fatalf("audit %+v", all)
	}
	byActor := get("actor_id=" + idString(bob.User.ID) + "&action=site.create")
	if byActor.Total != 1 || byActor.Items[0].ActorEmail != "bob@pp.test" || *byActor.Items[0].ActorID != idString(bob.User.ID) ||
		byActor.Items[0].Meta["title"] != "Audited" || byActor.Items[0].Target == "" {
		t.Fatalf("by actor %+v", byActor)
	}
	if got := get("action=site.create&from=2026-11-13"); got.Total != 1 {
		t.Fatalf("from date %+v", got)
	}
	if got := get("action=site.create&to=2026-11-12"); got.Total != 0 {
		t.Fatalf("to date %+v", got)
	}
	if got := get("action=site.create&to=2026-11-13"); got.Total != 1 {
		t.Fatalf("to date inclusive %+v", got)
	}
	if got := get("from=2026-11-12T00:00:00Z&to=2026-11-12T23:59:59%2B00:00&action=user.create"); got.Total != 1 {
		t.Fatalf("rfc3339 %+v", got)
	}
	if got := get("limit=1&offset=1"); len(got.Items) != 1 || got.Total != all.Total {
		t.Fatalf("page %+v", got)
	}
	for _, q := range []string{"from=yesterday", "to=2026-13-01", "actor_id=me", "offset=-2"} {
		wantError(t, h.do(http.MethodGet, "/api/v1/admin/audit?"+q, h.adminToken, nil), http.StatusBadRequest, domain.CodeInvalidInput)
	}
	// Entries without an actor have actor_id null and meta {}.
	if !strings.Contains(h.do(http.MethodGet, "/api/v1/admin/audit?action=user.bootstrap", h.adminToken, nil).Body.String(), `"meta":{`) {
		t.Fatal("meta missing")
	}
}

func TestAdminEndpointsAre404ForUsers(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	site := h.createSite(tok, "Mine", "")
	for _, rt := range (&api{}).routes() {
		if !strings.HasPrefix(rt.Path, "/admin/") {
			continue
		}
		path := Prefix + strings.NewReplacer("{ref}", site.ID, "{id}", "1", "{hostname}", "x.example.ru").Replace(rt.Path)
		for _, q := range []string{"", "?limit=bad&status=x&from=y"} {
			rec := h.do(rt.Method, path+q, tok, `{"bad json`)
			if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody {
				t.Errorf("%s %s: %d %s", rt.Method, path+q, rec.Code, rec.Body.String())
			}
		}
	}
}

const notFoundBody = `{"error":{"code":"not_found","message":"not found"}}` + "\n"
