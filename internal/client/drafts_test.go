package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDraftCalls(t *testing.T) {
	var seen []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/commit"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["draft"] != true {
				t.Errorf("commit body %v", body)
			}
			writeJSON(w, 201, map[string]any{"deploy_id": "d2", "deploy": map[string]any{"id": "d2", "draft": true}})
		case r.Method == http.MethodDelete:
			writeJSON(w, 200, map[string]any{"id": "s1", "draft": nil})
		default:
			_, _ = io.Copy(io.Discard, r.Body)
			writeJSON(w, 201, map[string]any{"deploy_id": "d1", "deploy": map[string]any{"id": "d1", "draft": true},
				"site": map[string]any{"id": "s1", "draft": map[string]any{"deploy_id": "d1",
					"created_at": "2026-11-12T10:00:00Z", "preview_url": "https://p-s1.example/"}}})
		}
	})
	ctx := context.Background()
	res, err := c.DeployWith(ctx, "s1", strings.NewReader("zip"), nil, DeployOptions{Draft: true})
	if err != nil || !res.Deploy.Draft || res.Site.Draft == nil || *res.Site.Draft.PreviewURL != "https://p-s1.example/" {
		t.Fatalf("deploy %+v %v", res, err)
	}
	if fr, err := c.CommitUploadWith(ctx, "up_1", nil, true); err != nil || !fr.Deploy.Draft {
		t.Fatalf("commit %+v %v", fr, err)
	}
	if _, err := c.PublishDraft(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if s, err := c.DiscardDraft(ctx, "s1"); err != nil || s.Draft != nil {
		t.Fatalf("discard %+v %v", s, err)
	}
	want := []string{
		"POST /api/v1/sites/s1/deploys?draft=true",
		"POST /api/v1/uploads/up_1/commit?",
		"POST /api/v1/sites/s1/draft/publish?",
		"DELETE /api/v1/sites/s1/draft?",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(seen, "\n"))
	}
}
