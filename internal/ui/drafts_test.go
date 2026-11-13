package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

// uploadDraft sends an archive with the draft box ticked, as the page's
// script does (the field goes before the archive).
func (h *harness) uploadDraft(ref string, files map[string]string) page {
	h.t.Helper()
	tok := h.token("/sites/" + ref)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField(draftField, "1")
	fw, err := mw.CreateFormFile("archive", "site.zip")
	if err != nil {
		h.t.Fatal(err)
	}
	_, _ = fw.Write(testenv.Zip(h.t, files))
	_ = mw.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/sites/"+ref+"/deploy", &buf)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set(csrfHeader, tok)
	return h.do(req)
}

func TestDraftBlockOnSitePage(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	page := "/sites/" + v.Site.ID
	live := h.deploy(alice, v.Site.ID, map[string]string{"index.html": "live"})
	body := h.get(page).body
	wantContains(t, body, `name="draft"`, "Сохранить как черновик")
	wantNotContains(t, body, `id="draft-h"`)

	p := h.uploadDraft(v.Site.ID, map[string]string{"index.html": "draft"})
	wantStatus(t, p, http.StatusOK)
	var reply deployReply
	if err := json.Unmarshal([]byte(p.body), &reply); err != nil || !reply.OK || !reply.Reload ||
		reply.URL != "https://p-"+v.Site.ID+".pp.test/" {
		t.Fatalf("reply %q %v", p.body, err)
	}
	cur, _ := h.env.Svc.GetSite(context.Background(), alice, v.Site.ID)
	if cur.Current == nil || cur.Current.ID != live.ID || cur.Draft == nil {
		t.Fatalf("site after draft upload %+v", cur)
	}
	body = h.get(page).body
	wantContains(t, body, `id="draft-h"`, "Открыть предпросмотр", "https://p-"+v.Site.ID+".pp.test/",
		page+"/draft/publish", page+"/draft/discard", "живыми данными", "badge--draft")

	wantStatus(t, h.submit(page, page+"/draft/publish", url.Values{}), http.StatusSeeOther)
	cur, _ = h.env.Svc.GetSite(context.Background(), alice, v.Site.ID)
	if cur.Draft != nil || cur.Current == nil || cur.Current.ID == live.ID {
		t.Fatalf("site after publish %+v", cur)
	}
	wantNotContains(t, h.get(page).body, `id="draft-h"`)

	wantStatus(t, h.uploadDraft(v.Site.ID, map[string]string{"index.html": "again"}), http.StatusOK)
	wantStatus(t, h.submit(page, page+"/draft/discard", url.Values{}), http.StatusSeeOther)
	if cur, _ = h.env.Svc.GetSite(context.Background(), alice, v.Site.ID); cur.Draft != nil {
		t.Fatal("draft kept after discard")
	}
	// Without a draft the action explains itself instead of failing silently.
	p = h.follow(h.submit(page, page+"/draft/publish", url.Values{}))
	wantContains(t, p.body, "нет черновика")
}

func TestDraftActionsOfStrangers(t *testing.T) {
	h := newHarness(t)
	alice := h.env.User("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	h.deploy(alice, v.Site.ID, map[string]string{"index.html": "live"})
	h.loginUser("eve@pp.test")
	wantStatus(t, h.submit("/", "/sites/"+v.Site.ID+"/draft/publish", url.Values{}), http.StatusNotFound)
	wantStatus(t, h.submit("/", "/sites/"+v.Site.ID+"/draft/discard", url.Values{}), http.StatusNotFound)
}
