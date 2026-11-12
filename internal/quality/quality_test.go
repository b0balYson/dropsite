package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const goodHead = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Cafe</title>
<meta name="description" content="Coffee"><link rel="stylesheet" href="/css/site.css"></head>`

func analyze(t *testing.T, files map[string]string, o Options) Report {
	t.Helper()
	r, err := Analyze(context.Background(), tree(t, files), o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCleanSiteScoresA(t *testing.T) {
	r := analyze(t, map[string]string{
		"index.html": goodHead + `<body><a href="/about">About</a> <a href="about">x</a> <a href="#top">top</a>
			<a href="mailto:a@b.c">mail</a> <a href="https://example.org/">ext link</a>
			<img src="img/logo.png" alt=""><img srcset="img/a.jpg 1x, img/b.jpg 2x" alt="a">
			<form action="/_form/lead"></form><script src="/_dropsite/altcha.js"></script>
			<link rel="icon" href="/favicon.ico"><link rel="canonical" href="https://other.example/x">
			<iframe src="https://www.youtube.com/embed/x"></iframe></body></html>`,
		"about.html":       goodHead + `<body><a href="./">home</a><a href="../../index.html">up</a></body></html>`,
		"css/site.css":     `@font-face{src:url("../fonts/inter.woff2")} body{background:url(data:image/png;base64,AA)}`,
		"fonts/inter.woff2": "font",
		"img/logo.png":     "png",
		"img/a.jpg":        "a",
		"img/b.jpg":        "b",
	}, Options{Generated: []string{"favicon.ico"}})
	if r.Score != 100 || r.Grade != "A" || r.Counts != (Counts{}) || r.Truncated {
		t.Fatalf("report %+v", r)
	}
	if r.Pages != 2 || r.Files != 7 || !strings.Contains(r.Summary, "no problems") {
		t.Fatalf("totals %+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "null") {
		t.Fatalf("null in JSON: %s", b)
	}
}

func TestBrokenLinksAndMissingAssets(t *testing.T) {
	r := analyze(t, map[string]string{
		"index.html": goodHead + `<body><a href="/abuot">x</a><a href="/abuot">again</a><a href="docs/">d</a>
			<a href="https://cafe.example.com/menu?x=1">own host</a><a href="price%20list">p</a>
			<img src="img/logo.png" alt="l"><video poster="poster.jpg"></video>
			<div style="background:url('/img/bg.jpg')"></div></body></html>`,
		"price list.html": goodHead + "</html>",
		"css/site.css":    `.a{background:url(missing.png)} @import "theme.css";`,
	}, Options{Hosts: []string{"cafe.example.com"}})
	wantIssues(t, "broken", r.BrokenLinks, []Issue{
		{Page: "index.html", Target: "/abuot"},
		{Page: "index.html", Target: "docs/"},
		{Page: "index.html", Target: "https://cafe.example.com/menu?x=1"},
	})
	wantIssues(t, "missing", r.MissingAssets, []Issue{
		{Page: "index.html", Target: "img/logo.png", Kind: "image"},
		{Page: "index.html", Target: "poster.jpg", Kind: "image"},
		{Page: "index.html", Target: "/img/bg.jpg", Kind: "image"},
		{Page: "css/site.css", Target: "theme.css", Kind: "stylesheet"},
		{Page: "css/site.css", Target: "missing.png", Kind: "image"},
	})
	if r.Counts.BrokenLinks != 3 || r.Counts.MissingAssets != 5 || r.Score != 60 || r.Grade != "C" {
		t.Fatalf("counts %+v score %d", r.Counts, r.Score)
	}
	s := Summarize(&r)
	if len(s.Issues) != 2 || !strings.Contains(s.Issues[0], "5 missing files, e.g. img/logo.png used in index.html") ||
		!strings.Contains(s.Issues[1], "3 broken links") {
		t.Fatalf("issues %q", s.Issues)
	}
}

func wantIssues(t *testing.T, what string, got, want []Issue) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\ngot  %+v\nwant %+v", what, got, want)
	}
}

func TestSPAModeSkipsPageLinksOnly(t *testing.T) {
	r := analyze(t, map[string]string{
		"index.html": goodHead + `<body><a href="/app/orders">o</a><img src="/x.png" alt="x"></body></html>`,
		"200.html":   goodHead + `</html>`,
	}, Options{})
	if r.Counts.BrokenLinks != 0 || r.Counts.MissingAssets != 3 { // x.png, css/site.css from both pages
		t.Fatalf("counts %+v", r.Counts)
	}
}

func TestMetadataAltAndExternal(t *testing.T) {
	r := analyze(t, map[string]string{
		"index.html": `<html><head><title>  </title>
			<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">
			<link rel="preload" as="font" href="//cdn.example.net/f.woff2">
			<script src="https://cdn.jsdelivr.net/npm/x@1/x.js"></script>
			<style>@import url("https://cdn.example.net/reset.css");</style></head>
			<body><img src="a.png"><img src="https://images.example.org/p.jpg" alt="p"></body></html>`,
		"a.png": "png",
	}, Options{})
	m := r.PagesMissing
	if !reflect.DeepEqual(m.Title, []string{"index.html"}) || len(m.Description) != 1 || len(m.Viewport) != 1 || len(m.Lang) != 1 {
		t.Fatalf("missing %+v", m)
	}
	wantIssues(t, "alt", r.ImagesWithoutAlt, []Issue{{Page: "index.html", Target: "a.png"}})
	wantIssues(t, "external", r.ExternalResources, []Issue{
		{Page: "index.html", Target: "https://fonts.googleapis.com/css2?family=Inter", Kind: "stylesheet"},
		{Page: "index.html", Target: "//cdn.example.net/f.woff2", Kind: "font"},
		{Page: "index.html", Target: "https://cdn.jsdelivr.net/npm/x@1/x.js", Kind: "script"},
		{Page: "index.html", Target: "https://cdn.example.net/reset.css", Kind: "stylesheet"},
		{Page: "index.html", Target: "https://images.example.org/p.jpg", Kind: "image"},
	})
	if !reflect.DeepEqual(r.ExternalHosts, []string{"cdn.example.net", "cdn.jsdelivr.net", "fonts.googleapis.com", "images.example.org"}) {
		t.Fatalf("hosts %v", r.ExternalHosts)
	}
	// 4 hosts (20) + title 5 + viewport 5 + description 2 + lang 2 + alt 1.
	if r.Score != 65 || r.Grade != "C" {
		t.Fatalf("score %d %s %+v", r.Score, r.Grade, r.Counts)
	}
	if s := Summarize(&r); len(s.Issues) != 5 || !strings.Contains(s.Issues[0], "loaded from other servers") {
		t.Fatalf("issues %q", s.Issues)
	}
}

func TestWeightAndLargestFiles(t *testing.T) {
	big := strings.Repeat("x", 2<<20)
	r := analyze(t, map[string]string{
		"index.html":   goodHead + `<body><img src="a.jpg" alt=""><img src="b.jpg" alt=""><img src="a.jpg" alt=""></body></html>`,
		"light.html":   goodHead + `</html>`,
		"css/site.css": `body{background:url(../bg.jpg)}`,
		"a.jpg":        big, "b.jpg": big, "bg.jpg": "bg",
	}, Options{})
	if r.Counts.HeavyPages != 1 || r.HeaviestPages[0].Page != "index.html" {
		t.Fatalf("heavy %+v %+v", r.Counts, r.HeaviestPages)
	}
	want := int64(len(goodHead)+len(`<body><img src="a.jpg" alt=""><img src="b.jpg" alt=""><img src="a.jpg" alt=""></body></html>`)) +
		2*int64(len(big)) + int64(len(`body{background:url(../bg.jpg)}`)) + 2
	if r.HeaviestPages[0].Bytes != want {
		t.Fatalf("weight %d, want %d", r.HeaviestPages[0].Bytes, want)
	}
	if r.LargestFiles[0].Path != "a.jpg" || r.LargestFiles[1].Path != "b.jpg" || len(r.LargestFiles) != 5 {
		t.Fatalf("largest %+v", r.LargestFiles)
	}
}

func TestListsAreCappedAndGeneratedDirIgnored(t *testing.T) {
	var b strings.Builder
	b.WriteString(goodHead + "<body>")
	for i := range 300 {
		fmt.Fprintf(&b, `<img src="img/%d.png">`, i)
	}
	r := analyze(t, map[string]string{
		"index.html": b.String(), "css/site.css": "",
		".dropsite/static/robots.txt": "x", ".dropsite/webp/x.html": "<a href=nope>",
	}, Options{})
	if r.Counts.ImagesWithoutAlt != 300 || len(r.ImagesWithoutAlt) != MaxListItems || r.Counts.MissingAssets != 300 ||
		len(r.MissingAssets) != MaxListItems {
		t.Fatalf("counts %+v", r.Counts)
	}
	if r.Pages != 1 || r.Files != 2 || r.Counts.BrokenLinks != 0 {
		t.Fatalf("generated dir analyzed: %+v", r)
	}
}

func TestSummarizeNil(t *testing.T) {
	if Summarize(nil) != nil {
		t.Fatal("summary of nil")
	}
}
