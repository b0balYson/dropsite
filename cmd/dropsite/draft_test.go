package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDeployDraftAndDraftCommand(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	v, err := r.env.Svc.CreateSite(ctx, ann, "Promo", "promo")
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEnv(t)
	e.env[envServer] = r.api.URL
	e.env[envToken] = r.env.Token(ann, "cli")
	preview := "https://p-" + v.Site.ID + ".pp.test/"

	res := e.run("deploy", siteDir(t, map[string]string{"index.html": "live"}), "--site", "promo")
	expectCode(t, res, 0)
	r.env.Clock.Advance(time.Second)
	res = e.run("deploy", siteDir(t, map[string]string{"index.html": "draft"}), "--site", "promo", "--draft")
	expectCode(t, res, 0)
	if strings.TrimSpace(res.stdout) != preview || !strings.Contains(res.stderr, "draft publish") {
		t.Fatalf("deploy --draft: stdout %q stderr %q", res.stdout, res.stderr)
	}
	if got, _ := r.env.Served("promo.pp.test", "index.html"); got != "live" {
		t.Fatalf("the draft went live: %q", got)
	}
	r.env.Clock.Advance(time.Second)
	res = e.run("deploy", siteDir(t, map[string]string{"index.html": "draft 2"}), "--site", "promo", "--draft", "--json")
	expectCode(t, res, 0)
	var out deployOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil || !out.Draft || out.PreviewURL != preview {
		t.Fatalf("json %q %v", res.stdout, err)
	}

	res = e.run("draft", "publish", "promo")
	expectCode(t, res, 0)
	if !strings.Contains(res.stdout, "is live") {
		t.Fatalf("draft publish: %q", res.stdout)
	}
	if got, _ := r.env.Served("promo.pp.test", "index.html"); got != "draft 2" {
		t.Fatalf("site serves %q", got)
	}
	if res := e.run("draft", "publish", "promo"); res.code == 0 || !strings.Contains(res.stderr+res.stdout, "draft") {
		t.Fatalf("publish without draft: %+v", res)
	}
	r.env.Clock.Advance(time.Second)
	expectCode(t, e.run("deploy", siteDir(t, map[string]string{"index.html": "x"}), "--site", "promo", "--draft"), 0)
	res = e.run("draft", "discard", "promo")
	expectCode(t, res, 0)
	if !strings.Contains(res.stdout, "Draft deleted") {
		t.Fatalf("draft discard: %q", res.stdout)
	}
	expectCode(t, e.run("draft"), 2)
	expectCode(t, e.run("draft", "show", "promo"), 2)
}
