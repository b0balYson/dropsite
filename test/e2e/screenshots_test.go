//go:build e2e

package e2e

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"testing"
)

// TestScreenshots runs only with E2E_RENDERER=1: control asks the Gotenberg
// renderer for a PNG of a release over the internal render network, and a
// visual diff of two releases reports the changed share.
func TestScreenshots(t *testing.T) {
	if !rendererStand() {
		t.Skip("set E2E_RENDERER=1 to run the screenshot renderer")
	}
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-screenshots")}

	st, body := owner.json(http.MethodPost, "/templates/cafe/sites", map[string]string{"title": "Shot Cafe", "lang": "en"})
	must(t, st, http.StatusCreated, body, "site from template")
	siteID := str(body["site"].(map[string]any), "id")
	first := str(body, "deploy_id")

	for _, device := range []string{"desktop", "mobile"} {
		req, _ := http.NewRequestWithContext(s.ctx, http.MethodGet,
			"https://"+apiHost+":"+httpsPort+"/api/v1/sites/"+siteID+"/screenshot?device="+device, nil)
		req.Header.Set("Authorization", "Bearer "+owner.token)
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("%s screenshot: %d %s %.300s", device, resp.StatusCode, resp.Header.Get("Content-Type"), raw)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s screenshot is not a PNG: %v", device, err)
		}
		if w := img.Bounds().Dx(); (device == "desktop" && w != 1280) || (device == "mobile" && w != 390) {
			t.Fatalf("%s screenshot width %d", device, w)
		}
	}

	st, body = owner.json(http.MethodPost, "/uploads", map[string]string{"site": siteID})
	must(t, st, http.StatusCreated, body, "begin upload")
	id := str(body, "id")
	st, body = owner.json(http.MethodPost, "/uploads/"+id+"/files", map[string]any{"files": []map[string]string{
		{"path": "index.html", "content": "<!doctype html><title>x</title><body style=\"background:#c00\"><h1>New</h1>"}}})
	must(t, st, http.StatusOK, body, "upload files")
	st, body = owner.json(http.MethodPost, "/uploads/"+id+"/commit", map[string]any{})
	must(t, st, http.StatusCreated, body, "commit")

	st, body = owner.json(http.MethodGet, "/sites/"+siteID+"/visual-diff?from="+first, nil)
	must(t, st, http.StatusOK, body, "visual diff")
	if changed, _ := body["changed_percent"].(float64); changed <= 0 {
		t.Fatalf("visual diff found no change: %v", body["changed_percent"])
	}
}
