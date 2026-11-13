package service

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestDeployFilesCreatesSite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	res, err := e.svc.DeployFiles(ctx, u, "", "Promo", []InlineFile{
		{Path: "index.html", Content: "<h1>hi</h1>"},
		{Path: "img/a.bin", Content: base64.StdEncoding.EncodeToString([]byte{0, 1, 2}), Encoding: "base64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.Source != domain.SourceMCP || res.Site.Site.Title != "Promo" || res.Deploy.FileCount != 2 {
		t.Fatalf("result: %+v", res.Deploy)
	}
	host := strings.TrimSuffix(strings.TrimPrefix(res.Site.URL, "https://"), "/")
	if got, err := e.served(host, "index.html"); err != nil || got != "<h1>hi</h1>" {
		t.Fatalf("served: %q %v", got, err)
	}
	// Update the same site by ref.
	res2, err := e.svc.DeployFiles(ctx, u, res.Site.Site.ID, "", []InlineFile{{Path: "index.html", Content: "v2"}})
	if err != nil || res2.Site.Site.ID != res.Site.Site.ID {
		t.Fatalf("redeploy: %v", err)
	}
}

func TestDeployFilesValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	cases := map[string][]InlineFile{
		"empty":     nil,
		"abs":       {{Path: "/etc/passwd", Content: "x"}},
		"dotdot":    {{Path: "a/../../x", Content: "x"}},
		"backslash": {{Path: "a\\b", Content: "x"}},
		"dup":       {{Path: "index.html", Content: "a"}, {Path: "index.html", Content: "b"}},
		"b64":       {{Path: "index.html", Content: "%%%", Encoding: "base64"}},
		"enc":       {{Path: "index.html", Content: "x", Encoding: "hex"}},
	}
	for name, files := range cases {
		_, err := e.svc.DeployFiles(ctx, u, "", "", files)
		wantCode(t, err, domain.CodeInvalidInput)
		_ = name
	}
	big := strings.Repeat("a", MaxInlineBytes)
	_, err := e.svc.DeployFiles(ctx, u, "", "", []InlineFile{{Path: "index.html", Content: big}, {Path: "b", Content: "x"}})
	wantCode(t, err, domain.CodeFilesTooLarge)
	if !strings.Contains(domain.AsError(err).Message, "upload_url") {
		t.Fatalf("message lacks advice: %v", err)
	}
	// No index.html: the regular pipeline rejects it.
	_, err = e.svc.DeployFiles(ctx, u, "", "", []InlineFile{{Path: "a.html", Content: "x"}})
	wantCode(t, err, domain.CodeArchiveNoIndex)
	// Someone else's site is not found.
	other := e.user("o@pp.test")
	v, err := e.svc.CreateSite(ctx, other, "O", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.DeployFiles(ctx, u, v.Site.ID, "", []InlineFile{{Path: "index.html", Content: "x"}})
	wantNotFound(t, err)
}
