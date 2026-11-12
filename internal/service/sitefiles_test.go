package service

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/sitefetch"
)

// published creates a site of owner with the given files and returns its
// ID, host and current release.
func (e *env) published(owner Actor, files map[string]string) (string, string, string) {
	e.t.Helper()
	ctx := context.Background()
	v, err := e.svc.CreateSite(ctx, owner, "Shop", "")
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.svc.Deploy(ctx, owner, v.Site.ID, zipOf(e.t, files), domain.SourceUI)
	if err != nil {
		e.t.Fatal(err)
	}
	return v.Site.ID, res.Site.Hostnames[0].Hostname, res.Deploy.ID
}

var shop = map[string]string{
	"index.html":   "<html><head></head><body>Price 100</body></html>",
	"css/site.css": "body{color:red}",
	"css/x.css":    "p{}",
	"img/logo.bin": "\x00\x01\x02",
}

func TestListAndReadSiteFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, dep := e.published(u, shop)

	l, err := e.svc.ListSiteFiles(ctx, u, id, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Deploy.ID != dep || !l.Current || l.TotalFiles != 4 || len(l.Files) != 4 || l.Files[0].Path != "css/site.css" ||
		len(l.Files[0].SHA256) != 64 || l.Files[0].Type != "text/css" {
		t.Fatalf("list: %+v", l)
	}
	if l, _ := e.svc.ListSiteFiles(ctx, u, id, "", "css/"); len(l.Files) != 2 || l.TotalFiles != 4 {
		t.Fatalf("prefix: %+v", l.Files)
	}

	r, err := e.svc.ReadSiteFiles(ctx, u, id, dep, []string{"index.html", "css/*.css", "img/logo.bin", "nope.txt", "css/*.js"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Deploy.ID != dep || len(r.Files) != 4 || !r.Files[0].Text || !strings.Contains(string(r.Files[0].Content), "Price 100") ||
		r.Files[3].Text || string(r.Files[3].Content) != "\x00\x01\x02" {
		t.Fatalf("read: %+v", r)
	}
	if strings.Join(r.Missing, ",") != "css/*.js,nope.txt" {
		t.Fatalf("missing: %v", r.Missing)
	}
}

func TestReadSiteFilesStaysInsideRelease(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, dep := e.published(u, shop)
	for _, p := range []string{"../../state/state.db", "a/../../x", "a\\b", "", "//x"} {
		if _, err := e.svc.ReadSiteFiles(ctx, u, id, "", []string{p}); err == nil || domain.AsError(err).Code != domain.CodeInvalidInput {
			t.Errorf("%q: %v", p, err)
		}
	}
	// A leading slash means the site root, never the server's file system.
	if r, err := e.svc.ReadSiteFiles(ctx, u, id, "", []string{"/etc/passwd", "/index.html"}); err != nil ||
		len(r.Files) != 1 || r.Files[0].Path != "index.html" || r.Missing[0] != "etc/passwd" {
		t.Fatalf("leading slash: %+v %v", r, err)
	}
	// A symbolic link planted in the release is neither listed nor followed.
	secret := filepath.Join(e.root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(e.root, "sites", id, "releases", dep)
	if err := os.Symlink(secret, filepath.Join(rel, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../secret.txt", filepath.Join(rel, "rel.txt")); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.ReadSiteFiles(ctx, u, id, "", []string{"link.txt", "rel.txt", "*.txt", "css"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 0 || len(r.Missing) != 4 {
		t.Fatalf("links read: %+v", r)
	}
	l, _ := e.svc.ListSiteFiles(ctx, u, id, "", "")
	for _, f := range l.Files {
		if strings.HasSuffix(f.Path, ".txt") {
			t.Fatalf("link listed: %s", f.Path)
		}
	}
	if _, err := e.svc.ReadSiteFiles(ctx, u, id, "", make([]string, MaxReadPaths+1)); err == nil {
		t.Fatal("too many paths accepted")
	}
}

func TestSiteFilesForeignAndUnpublished(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("u@pp.test"), e.user("o@pp.test")
	id, _, dep := e.published(u, shop)
	if _, err := e.svc.ListSiteFiles(ctx, other, id, "", ""); err == nil {
		t.Fatal("foreign list")
	} else {
		wantNotFound(t, err)
	}
	_, err := e.svc.ReadSiteFiles(ctx, other, id, dep, []string{"index.html"})
	wantNotFound(t, err)
	_, err = e.svc.UpdateSiteFiles(ctx, other, id, dep, FileChanges{Put: []InlineFile{{Path: "index.html", Content: "x"}}})
	wantNotFound(t, err)
	// A release of another site is not found either.
	oid, _, odep := e.published(other, shop)
	_, err = e.svc.ListSiteFiles(ctx, u, id, odep, "")
	wantNotFound(t, err)
	_, err = e.svc.ListSiteFiles(ctx, u, id, "../"+odep, "")
	wantNotFound(t, err)
	_ = oid
	v, _ := e.svc.CreateSite(ctx, u, "Empty", "")
	_, err = e.svc.ListSiteFiles(ctx, u, v.Site.ID, "", "")
	wantCode(t, err, domain.CodeSiteNotPublished)
}

func TestReadSiteFilesLimitsAndCounters(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	big := strings.Repeat("a", 200<<10)
	files := map[string]string{
		"index.html": "<html><head></head><body>hi</body></html>", "a.txt": big, "b.txt": big,
		"huge.txt": strings.Repeat("b", MaxReadBytes+1), "photo.jpg": "\xff\xd8" + strings.Repeat("\x00", 70<<10),
	}
	id, _, _ := e.published(u, files)
	if _, err := e.svc.SetSiteCounters(ctx, u, id, domain.Counters{MetrikaID: "12345678"}, domain.SourceMCP); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.ReadSiteFiles(ctx, u, id, "", []string{"index.html", "a.txt", "b.txt", "huge.txt", "photo.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Files[0].Content), "dropsite:counters") || !strings.Contains(string(r.Files[0].Content), "hi") {
		t.Fatalf("counter block not stripped: %s", r.Files[0].Content)
	}
	if r.Files[1].Content == nil || r.Files[2].Content != nil || !r.Truncated || !strings.Contains(r.Files[2].Note, "separate call") {
		t.Fatalf("budget: %+v %+v", r.Files[1].Note, r.Files[2].Note)
	}
	if r.Files[3].Content != nil || r.Files[4].Content != nil || r.Files[4].SHA256 == "" || r.Files[4].Note == "" {
		t.Fatalf("large files: %+v %+v", r.Files[3], r.Files[4])
	}
}

func TestUpdateSiteFilesKeepsUnchangedFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, base := e.published(u, shop)
	res, err := e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{
		Put: []InlineFile{
			{Path: "index.html", Content: "<html><body>Price 120</body></html>"},
			{Path: "img/new.bin", Content: base64.StdEncoding.EncodeToString([]byte{9, 9}), Encoding: "base64"},
		},
		Delete: []string{"img/logo.bin", "css/x.css"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.Source != domain.SourceMCP || res.Base != base || res.Deploy.FileCount != 3 || res.Deleted != 2 || res.Written != 2 {
		t.Fatalf("result: %+v", res)
	}
	for p, want := range map[string]string{"index.html": "Price 120", "css/site.css": "body{color:red}", "img/new.bin": "\x09\x09"} {
		if got, err := e.served(host, p); err != nil || !strings.Contains(got, want) {
			t.Errorf("%s: %q %v", p, got, err)
		}
	}
	for _, p := range []string{"img/logo.bin", "css/x.css"} {
		if _, err := e.served(host, p); err == nil {
			t.Errorf("%s still served", p)
		}
	}
	// The base release is untouched and rollback restores it.
	if _, err := e.svc.Rollback(ctx, u, id, base); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.served(host, "index.html"); !strings.Contains(got, "Price 100") {
		t.Fatalf("rollback: %q", got)
	}
	ds, _, _ := e.svc.ListDeploys(ctx, u, id)
	if len(ds) != 2 {
		t.Fatalf("releases: %d", len(ds))
	}
}

func TestUpdateSiteFilesValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, base := e.published(u, shop)
	put := []InlineFile{{Path: "index.html", Content: "x"}}
	cases := map[string]struct {
		base string
		ch   FileChanges
		code string
	}{
		"no base":        {"", FileChanges{Put: put}, domain.CodeInvalidInput},
		"nothing":        {base, FileChanges{}, domain.CodeInvalidInput},
		"traversal":      {base, FileChanges{Put: []InlineFile{{Path: "../x", Content: "x"}}}, domain.CodeInvalidInput},
		"secret":         {base, FileChanges{Put: []InlineFile{{Path: ".env", Content: "K=1"}}}, domain.CodeInvalidInput},
		"key":            {base, FileChanges{Put: []InlineFile{{Path: "certs/site.key", Content: "x"}}}, domain.CodeInvalidInput},
		"ignored":        {base, FileChanges{Put: []InlineFile{{Path: ".git/config", Content: "x"}}}, domain.CodeInvalidInput},
		"put and delete": {base, FileChanges{Put: put, Delete: []string{"index.html"}}, domain.CodeInvalidInput},
		"delete missing": {base, FileChanges{Delete: []string{"nope.css"}}, domain.CodeInvalidInput},
		"delete index":   {base, FileChanges{Delete: []string{"index.html"}}, domain.CodeArchiveNoIndex},
		"too big":        {base, FileChanges{Put: []InlineFile{{Path: "a.txt", Content: strings.Repeat("a", MaxInlineBytes+1)}}}, domain.CodeFilesTooLarge},
		"fetch html":     {base, FileChanges{Fetch: []FetchSpec{{URL: "https://cdn.example.com/a", Path: "a.html"}}}, domain.CodeInvalidInput},
		"fetch http":     {base, FileChanges{Fetch: []FetchSpec{{URL: "http://cdn.example.com/a.png", Path: "a.png"}}}, domain.CodeFetchFailed},
		"fetch dup":      {base, FileChanges{Put: []InlineFile{{Path: "a.png", Content: "x"}}, Fetch: []FetchSpec{{URL: "https://cdn.example.com/a.png", Path: "a.png"}}}, domain.CodeInvalidInput},
		"stale":          {"20200101-000000-aaaaaa", FileChanges{Put: put}, domain.CodeDeployConflict},
	}
	for name, c := range cases {
		_, err := e.svc.UpdateSiteFiles(ctx, u, id, c.base, c.ch)
		if de := domain.AsError(err); err == nil || de.Code != c.code {
			t.Errorf("%s: want %s, got %v", name, c.code, err)
		}
	}
	ds, _, _ := e.svc.ListDeploys(ctx, u, id)
	if len(ds) != 1 {
		t.Fatalf("a failed edit published something: %d releases", len(ds))
	}
}

func TestUpdateSiteFilesConflicts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, base := e.published(u, shop)
	// Someone republished after the assistant read the files.
	if _, err := e.svc.Deploy(ctx, u, id, zipOf(t, map[string]string{"index.html": "v2"}), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{Put: []InlineFile{{Path: "index.html", Content: "x"}}})
	wantCode(t, err, domain.CodeDeployConflict)
	if de := domain.AsError(err); de.Kind != domain.KindConflict || de.Params["current"] == base {
		t.Fatalf("conflict: %+v", de)
	}
	if got, _ := e.served(host, "index.html"); got != "v2" {
		t.Fatalf("stale edit published: %q", got)
	}
}

func TestUpdateSiteFilesParallel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, base := e.published(u, shop)
	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			_, errs[i] = e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{
				Put: []InlineFile{{Path: "index.html", Content: strings.Repeat("v", i+1)}},
			})
		})
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case domain.AsError(err).Code != domain.CodeDeployConflict:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d parallel edits succeeded, want exactly 1", ok)
	}
}

func TestUpdateSiteFilesOwnerLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, base := e.published(u, shop)
	small := int64(1000)
	if _, err := e.svc.SetUserLimits(ctx, e.admin, u.User.ID, domain.LimitOverrides{MaxFileBytes: &small}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{Put: []InlineFile{{Path: "a.txt", Content: strings.Repeat("a", 2000)}}})
	wantCode(t, err, domain.CodeArchiveFileTooBig)
}

type fakeFetcher struct {
	err   error
	calls int
	max   int64
}

func (f *fakeFetcher) Fetch(_ context.Context, reqs []sitefetch.Request, maxFile int64) ([]sitefetch.File, error) {
	f.calls++
	f.max = maxFile
	if f.err != nil {
		return nil, f.err
	}
	out := make([]sitefetch.File, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, sitefetch.File{URL: r.URL, Path: r.Path, Data: []byte("PNG"), ContentType: "image/png", OriginalBytes: 9, Resized: true})
	}
	return out, nil
}

func TestUpdateSiteFilesFetch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, base := e.published(u, shop)
	ff := &fakeFetcher{}
	e.svc.fetcher = ff
	res, err := e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{
		Fetch: []FetchSpec{{URL: "https://cdn.example.com/hero.png", Path: "img/hero.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fetched) != 1 || !res.Fetched[0].Resized || ff.max != 20<<20 {
		t.Fatalf("fetched: %+v max %d", res.Fetched, ff.max)
	}
	if got, _ := e.served(host, "img/hero.png"); got != "PNG" {
		t.Fatalf("served: %q", got)
	}
	ff.err = &sitefetch.Error{URL: "https://cdn.example.com/x.png", Reason: "the address points to a private or reserved network"}
	_, err = e.svc.UpdateSiteFiles(ctx, u, id, res.Deploy.ID, FileChanges{
		Fetch: []FetchSpec{{URL: "https://cdn.example.com/x.png", Path: "x.png"}},
	})
	wantCode(t, err, domain.CodeFetchFailed)
	// A stale base fails before anything is downloaded.
	calls := ff.calls
	_, err = e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{Fetch: []FetchSpec{{URL: "https://cdn.example.com/y.png", Path: "y.png"}}})
	wantCode(t, err, domain.CodeDeployConflict)
	if ff.calls != calls {
		t.Fatal("fetched for a stale base")
	}
}
