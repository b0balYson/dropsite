//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
)

// deployDraft publishes an archive as the site's draft.
func (a *api) deployDraft(ref string, archive []byte) (int, map[string]any) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("archive", "site.zip")
	_, _ = w.Write(archive)
	_ = mw.Close()
	return a.do(http.MethodPost, "/sites/"+ref+"/deploys?draft=true", &buf, mw.FormDataContentType())
}

// TestDrafts: a draft is served at the site's stable preview address (in the
// default ondemand mode too) with noindex while the site keeps its release;
// publishing switches the site without a restart, and the preview then
// answers 404 over the same certificate.
func TestDrafts(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-drafts")}

	st, body := owner.json(http.MethodPost, "/sites", map[string]string{"title": "E2E Drafts"})
	must(t, st, http.StatusCreated, body, "create site")
	siteID := str(body, "id")
	host := hostOf(str(body, "url"))
	st, body = owner.deploy(siteID, makeZip(t, site(map[string]string{"index.html": "<p>Live version</p>"})...))
	must(t, st, http.StatusCreated, body, "deploy")
	s.expectBody(host, "/", http.StatusOK, "Live version")

	st, body = owner.deployDraft(siteID, makeZip(t, site(map[string]string{"index.html": "<p>Draft version</p>"})...))
	must(t, st, http.StatusCreated, body, "draft")
	draft, _ := body["site"].(map[string]any)["draft"].(map[string]any)
	if draft == nil || str(draft, "preview_url") == "" {
		t.Fatalf("no draft preview: %v", body)
	}
	preview := hostOf(str(draft, "preview_url"))
	if preview != "p-"+siteID+"."+baseDomain {
		t.Fatalf("preview host %q", preview)
	}
	s.expectBody(host, "/", http.StatusOK, "Live version")
	s.expectBody(preview, "/", http.StatusOK, "Draft version")
	s.eventually("noindex on the draft preview", func() error {
		resp, _, err := s.get(preview, "/")
		if err != nil {
			return err
		}
		if got := resp.Header.Get("X-Robots-Tag"); got != "noindex" {
			return fmt.Errorf("X-Robots-Tag %q", got)
		}
		return nil
	})

	st, body = owner.json(http.MethodPost, "/sites/"+siteID+"/draft/publish", nil)
	must(t, st, http.StatusOK, body, "publish draft")
	s.expectBody(host, "/", http.StatusOK, "Draft version")
	s.eventually("preview without a draft", func() error {
		resp, _, err := s.get(preview, "/")
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	})
	st, body = owner.json(http.MethodPost, "/sites/"+siteID+"/draft/publish", nil)
	if st != http.StatusBadRequest || errCode(body) != "no_draft" {
		t.Fatalf("publish without a draft: %d %v", st, body)
	}
}
