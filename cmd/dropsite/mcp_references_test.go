package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/api"
	"github.com/b0balYson/dropsite/internal/mcp/remote"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func newReferenceRig(t *testing.T, opts ...testenv.Option) *parityRig {
	t.Helper()
	env := testenv.New(t, append([]testenv.Option{testenv.WithPageFetcher(testenv.NewStaticPages())}, opts...)...)
	r := &parityRig{t: t, env: env}
	r.api = httptest.NewServer(api.New(env.Svc, nil))
	t.Cleanup(r.api.Close)
	r.remote = httptest.NewServer(remote.New(env.Svc, remote.Options{}))
	t.Cleanup(r.remote.Close)
	return r
}

func TestStdioInspectReference(t *testing.T) {
	r := newReferenceRig(t)
	tok := r.env.Token(r.env.User("ann@pp.test"), "cli")
	local, web := r.stdio(tok), r.web(tok)
	out := same(t, local, web, "inspect_reference", map[string]any{"url": testenv.ReferenceURL})
	if out["color_scheme"] != "dark" || out["screenshot"] != "unavailable" || !strings.Contains(out["advice"].(string), "copy") {
		t.Fatalf("brief: %v", out)
	}
	if comps, _ := out["components"].([]any); len(comps) == 0 {
		t.Fatalf("components: %v", out)
	}
	same(t, local, web, "inspect_reference", map[string]any{"url": "http://reference.example/"})
	same(t, local, web, "inspect_reference", map[string]any{"url": testenv.ReferenceURL + "missing"})
	wantToolErr(t, local, "inspect_reference", map[string]any{"url": testenv.ReferenceURL + "missing"}, "fetch_failed")
}

func TestStdioInspectReferenceScreenshot(t *testing.T) {
	r := newReferenceRig(t, testenv.WithRenderer(whiteRenderer{}))
	tok := r.env.Token(r.env.User("ann@pp.test"), "cli")
	for _, cs := range []*sdk.ClientSession{r.stdio(tok), r.web(tok)} {
		img := imageOf(t, cs, "inspect_reference", map[string]any{"url": testenv.ReferenceURL})
		if img.MIMEType != "image/png" || !strings.HasPrefix(string(img.Data), "\x89PNG") {
			t.Fatalf("image %s", img.MIMEType)
		}
	}
}
