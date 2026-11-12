package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func (e *env) sessionDirs() []string {
	e.t.Helper()
	m, _ := filepath.Glob(filepath.Join(e.svc.uploadDir, uploadSessionsDir, "up_*"))
	return m
}

func TestUploadReplaceCreatesSite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	info, err := e.svc.BeginUpload(ctx, u, "", "Big site", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != UploadReplace || !strings.HasPrefix(info.ID, "up_") || info.MaxBytes == 0 {
		t.Fatalf("info: %+v", info)
	}
	chunk := strings.Repeat("x", 900<<10)
	for i, p := range []string{"index.html", "a/1.txt", "a/2.txt"} {
		got, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: p, Content: chunk[:len(chunk)-i]}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Files != i+1 {
			t.Fatalf("files: %d", got.Files)
		}
	}
	// Re-sending a path replaces the file.
	if got, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "index.html", Content: "<h1>big</h1>"}}, nil); err != nil || got.Files != 3 {
		t.Fatalf("replace: %+v %v", got, err)
	}
	_, err = e.svc.CommitUpload(ctx, u, info.ID, []string{"a"})
	wantCode(t, err, domain.CodeInvalidInput) // delete is for patch uploads
	info2, _ := e.svc.BeginUpload(ctx, u, "", "Big site", UploadReplace, "")
	for _, p := range []string{"index.html", "a/1.txt", "a/2.txt"} {
		if _, _, err := e.svc.UploadFiles(ctx, u, info2.ID, []InlineFile{{Path: p, Content: chunk}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.svc.CommitUpload(ctx, u, info2.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Site.Site.Title != "Big site" || res.Deploy.FileCount != 3 || res.Deploy.Source != domain.SourceMCP {
		t.Fatalf("commit: %+v", res)
	}
	if len(e.sessionDirs()) != 0 {
		t.Fatal("session directories left behind")
	}
	_, _, err = e.svc.UploadFiles(ctx, u, info2.ID, []InlineFile{{Path: "b.txt", Content: "x"}}, nil)
	wantCode(t, err, domain.CodeUploadExpired)
}

func TestUploadPatch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, base := e.published(u, shop)
	_, err := e.svc.BeginUpload(ctx, u, id, "", UploadPatch, "")
	wantCode(t, err, domain.CodeInvalidInput)
	_, err = e.svc.BeginUpload(ctx, u, "", "", UploadPatch, base)
	wantCode(t, err, domain.CodeInvalidInput)
	info, err := e.svc.BeginUpload(ctx, u, id, "", UploadPatch, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "img/big.bin", Content: "new"}}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.CommitUpload(ctx, u, info.ID, []string{"img/logo.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.FileCount != 4 || res.Deleted != 1 {
		t.Fatalf("patch: %+v", res)
	}
	if got, _ := e.served(host, "css/site.css"); got != "body{color:red}" {
		t.Fatalf("unchanged file lost: %q", got)
	}
	// A patch on a stale base conflicts at commit and closes the session.
	info, _ = e.svc.BeginUpload(ctx, u, id, "", UploadPatch, res.Deploy.ID)
	_, _, _ = e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "x.txt", Content: "x"}}, nil)
	if _, err := e.svc.Deploy(ctx, u, id, zipOf(t, map[string]string{"index.html": "v3"}), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CommitUpload(ctx, u, info.ID, nil)
	wantCode(t, err, domain.CodeDeployConflict)
	_, err = e.svc.CommitUpload(ctx, u, info.ID, nil)
	wantCode(t, err, domain.CodeUploadExpired)
}

func TestUploadSessionLimitsAndOwnership(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("u@pp.test"), e.user("o@pp.test")
	var ids []string
	for range MaxUploadSessions {
		info, err := e.svc.BeginUpload(ctx, u, "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
	}
	_, err := e.svc.BeginUpload(ctx, u, "", "", "", "")
	wantCode(t, err, domain.CodeUploadLimit)
	// Another user's sessions do not count, and theirs are invisible.
	if _, err := e.svc.BeginUpload(ctx, other, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.UploadFiles(ctx, other, ids[0], []InlineFile{{Path: "index.html", Content: "x"}}, nil)
	wantCode(t, err, domain.CodeUploadExpired)
	_, err = e.svc.CommitUpload(ctx, other, ids[0], nil)
	wantCode(t, err, domain.CodeUploadExpired)
	// Paths are checked when uploaded.
	for _, p := range []string{"../x", ".env", "a\\b"} {
		_, _, err := e.svc.UploadFiles(ctx, u, ids[0], []InlineFile{{Path: p, Content: "x"}}, nil)
		wantCode(t, err, domain.CodeInvalidInput)
	}
	// A file where a folder was uploaded before is refused.
	if _, _, err := e.svc.UploadFiles(ctx, u, ids[0], []InlineFile{{Path: "a/b.txt", Content: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.UploadFiles(ctx, u, ids[0], []InlineFile{{Path: "a", Content: "x"}}, nil)
	wantCode(t, err, domain.CodeInvalidInput)
	// Foreign site cannot be targeted.
	oid, _, obase := e.published(other, shop)
	_, err = e.svc.BeginUpload(ctx, u, oid, "", UploadPatch, obase)
	wantNotFound(t, err)
}

func TestUploadOwnerLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	site, files, fileBytes := int64(3000), int64(2), int64(2000)
	if _, err := e.svc.SetUserLimits(ctx, e.admin, u.User.ID, domain.LimitOverrides{
		MaxSiteBytes: &site, MaxFiles: &files, MaxFileBytes: &fileBytes,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := e.svc.BeginUpload(ctx, u, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "index.html", Content: strings.Repeat("a", 2001)}}, nil)
	wantCode(t, err, domain.CodeArchiveFileTooBig)
	if _, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "index.html", Content: strings.Repeat("a", 2000)}}, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "b.txt", Content: strings.Repeat("a", 1001)}}, nil)
	wantCode(t, err, domain.CodeArchiveTooLarge)
	if _, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "b.txt", Content: "b"}}, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "c.txt", Content: "c"}}, nil)
	wantCode(t, err, domain.CodeArchiveTooMany)
}

func TestUploadSessionTTLAndCleanup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	a, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	b, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	if _, _, err := e.svc.UploadFiles(ctx, u, a.ID, []InlineFile{{Path: "index.html", Content: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(UploadSessionTTL - time.Minute)
	// Activity extends a session.
	if _, _, err := e.svc.UploadFiles(ctx, u, b.ID, []InlineFile{{Path: "index.html", Content: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(2 * time.Minute)
	_, _, err := e.svc.UploadFiles(ctx, u, a.ID, []InlineFile{{Path: "index.html", Content: "y"}}, nil)
	wantCode(t, err, domain.CodeUploadExpired)
	if len(e.sessionDirs()) != 1 {
		t.Fatalf("expired session dir kept: %v", e.sessionDirs())
	}
	e.clock.Advance(UploadSessionTTL + time.Minute)
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.sessionDirs()) != 0 || len(e.svc.uploads.sessions) != 0 {
		t.Fatal("housekeeping kept an expired session")
	}
	// Start removes leftovers of a previous process.
	left := filepath.Join(e.svc.uploadDir, uploadSessionsDir, "up_stale")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatal("start kept stale session directory")
	}
}

func TestUploadFetch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	e.svc.fetcher = &fakeFetcher{}
	info, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	got, fetched, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "index.html", Content: "x"}},
		[]FetchSpec{{URL: "https://cdn.example.com/f.woff2", Path: "fonts/f.woff2"}})
	if err != nil || got.Files != 2 || len(fetched) != 1 {
		t.Fatalf("upload fetch: %+v %+v %v", got, fetched, err)
	}
}
