package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestStdioQualityTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	if _, err := r.env.Svc.CreateSite(ctx, ann, "Coffee", "coffee"); err != nil {
		t.Fatal(err)
	}
	wantToolErr(t, local, "get_quality_report", map[string]any{"site": "coffee"}, "no published release")
	same(t, local, web, "get_quality_report", map[string]any{"site": "coffee"})

	page := `<html><body><a href="/abuot">x</a><script src="https://cdn.example.net/x.js"></script></body></html>`
	res, err := r.env.Svc.Deploy(ctx, ann, "coffee", bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": page})),
		domain.SourceAPI)
	if err != nil {
		t.Fatal(err)
	}
	got := same(t, local, web, "get_quality_report", map[string]any{"site": "coffee"})
	rep, _ := got["report"].(map[string]any)
	if got["deploy_id"] != res.Deploy.ID || got["current"] != true || rep == nil ||
		rep["counts"].(map[string]any)["broken_links"] != float64(1) || !strings.Contains(got["summary"].(string), "broken link") {
		t.Fatalf("report: %v", got)
	}
	same(t, local, web, "get_quality_report", map[string]any{"site": "coffee", "deploy_id": res.Deploy.ID})
	same(t, local, web, "get_quality_report", map[string]any{"site": "coffee", "deploy_id": "nope"})

	// Publication results carry the short report on both servers.
	base := res.Deploy.ID
	for _, cs := range []*sdk.ClientSession{local, web} {
		out := mustTool(t, cs, "update_site_files", map[string]any{"site": "coffee", "base_deploy_id": base,
			"files": []map[string]string{{"path": "abuot.html", "content": "<p>about</p>"}}})
		q, _ := out["quality"].(map[string]any)
		if q == nil || !strings.Contains(out["summary"].(string), "Quality ") ||
			!strings.Contains(strings.Join(toStrings(q["issues"]), "\n"), "another server") {
			t.Fatalf("update result: %v", out)
		}
		base = out["deploy_id"].(string)
	}
	for _, cs := range []*sdk.ClientSession{local, web} {
		out := mustTool(t, cs, "rollback", map[string]any{"site": "coffee", "deploy_id": res.Deploy.ID})
		if q, _ := out["quality"].(map[string]any); q == nil || q["grade"] == "" {
			t.Fatalf("rollback result: %v", out)
		}
	}
}

func toStrings(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
