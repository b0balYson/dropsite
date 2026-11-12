package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestCloneCommand(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	v, err := r.env.Svc.CreateSite(ctx, ann, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.env.Svc.Deploy(ctx, ann, v.Site.ID, bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": "hi"})), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	e := newTestEnv(t)
	e.env[envServer] = r.api.URL
	e.env[envToken] = r.env.Token(ann, "cli")

	res := e.run("clone", "promo", "--title", "Promo June")
	expectCode(t, res, 0)
	if !strings.Contains(res.stdout, "Copied to new site") || !strings.Contains(res.stdout, "https://") {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = e.run("clone", "promo", "--json")
	expectCode(t, res, 0)
	var out deployOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil || out.SiteID == "" || out.SiteID == v.Site.ID {
		t.Fatalf("json %q %v", res.stdout, err)
	}
	expectCode(t, e.run("clone"), 2)
	if res := e.run("clone", "nope"); res.code == 0 || !strings.Contains(res.stderr, "nope") {
		t.Fatalf("missing site: %+v", res)
	}
}
