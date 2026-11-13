package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

type tarEntry struct {
	name string
	body string
	kind byte
}

func tarOf(t *testing.T, gz bool, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var w = &buf
	var zw *gzip.Writer
	tw := tar.NewWriter(w)
	if gz {
		zw = gzip.NewWriter(w)
		tw = tar.NewWriter(zw)
	}
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: e.kind}
		switch e.kind {
		case 0:
			h.Typeflag = tar.TypeReg
		case tar.TypeDir, tar.TypeSymlink:
			h.Size, h.Linkname = 0, "target"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return &buf
}

func linkToken(t *testing.T, info UploadInfo) string {
	t.Helper()
	i := strings.LastIndex(info.LinkURL, UploadLinkPath)
	if i < 0 || !strings.HasPrefix(info.LinkURL, "https://") {
		t.Fatalf("link url %q", info.LinkURL)
	}
	return info.LinkURL[i+len(UploadLinkPath):]
}

func TestUploadLinkIssued(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	info, err := e.svc.BeginUpload(ctx, u, "", "Site", "", "")
	if err != nil {
		t.Fatal(err)
	}
	token := linkToken(t, info)
	if len(token) != uploadLinkTokenLen || !tokenShape(token) {
		t.Fatalf("token %q", token)
	}
	if !info.LinkExpiresAt.Equal(e.clock.Now().Add(UploadLinkTTL)) {
		t.Fatalf("link expires %v", info.LinkExpiresAt)
	}
	// Only the hash is kept.
	sess := e.svc.uploads.sessions[info.ID]
	if bytes.Contains(sess.link[:], []byte(token)) || e.svc.uploads.links[sess.link] != info.ID {
		t.Fatal("link not stored as hash")
	}
	other, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	if linkToken(t, other) == token {
		t.Fatal("links repeat")
	}
	// Later calls do not reveal the link again.
	got, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "a.txt", Content: "a"}}, nil)
	if err != nil || got.LinkURL != "" {
		t.Fatalf("upload_files: %+v %v", got, err)
	}
}

func TestUploadLinkPutAndCommit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	info, _ := e.svc.BeginUpload(ctx, u, "", "Linked", "", "")
	token := linkToken(t, info)
	rec, err := e.svc.PutLinkFile(ctx, token, "10.1.1.1", "index.html", strings.NewReader("<h1>one</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Files != 1 || rec.Upload.Files != 1 {
		t.Fatalf("receipt %+v", rec)
	}
	// Sending a path again replaces it.
	if _, err := e.svc.PutLinkFile(ctx, token, "10.1.1.1", "index.html", strings.NewReader("<h1>two</h1>")); err != nil {
		t.Fatal(err)
	}
	rec, err = e.svc.PutLinkFile(ctx, token, "10.1.1.1", "img/a.png", strings.NewReader("png"))
	if err != nil || rec.Upload.Files != 2 || rec.Upload.Bytes != int64(len("<h1>two</h1>png")) {
		t.Fatalf("receipt %+v %v", rec, err)
	}
	for _, bad := range []string{"../x", "/abs", ".env", "keys/id_rsa", ".git/config", "a//b"} {
		_, err := e.svc.PutLinkFile(ctx, token, "10.1.1.1", bad, strings.NewReader("x"))
		wantCode(t, err, domain.CodeInvalidInput)
	}
	res, err := e.svc.CommitUpload(ctx, u, info.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy.FileCount != 2 {
		t.Fatalf("commit %+v", res.Deploy)
	}
	body, err := e.served(res.Site.Hostnames[0].Hostname, "index.html")
	if err != nil || body != "<h1>two</h1>" {
		t.Fatalf("served %q %v", body, err)
	}
	// After commit the link is dead.
	_, err = e.svc.PutLinkFile(ctx, token, "10.1.1.1", "b.txt", strings.NewReader("x"))
	wantCode(t, err, domain.CodeUploadLinkInvalid)
	if _, err := e.svc.UploadLinkInfo(ctx, token, "10.1.1.1"); domain.AsError(err).Code != domain.CodeUploadLinkInvalid {
		t.Fatalf("info after commit: %v", err)
	}
	if len(e.svc.uploads.links) != 0 {
		t.Fatal("link kept after commit")
	}
	left, _ := filepath.Glob(filepath.Join(e.svc.sessionsRoot(), stagingPrefix+"*"))
	if len(left) != 0 {
		t.Fatalf("staging left: %v", left)
	}
}

func TestUploadLinkArchive(t *testing.T) {
	for _, gz := range []bool{false, true} {
		e := newEnv(t)
		ctx := context.Background()
		u := e.user("u@pp.test")
		info, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
		token := linkToken(t, info)
		body := tarOf(t, gz,
			tarEntry{name: "./", kind: tar.TypeDir},
			tarEntry{name: "./index.html", body: "<h1>hi</h1>"},
			tarEntry{name: "./css/", kind: tar.TypeDir},
			tarEntry{name: "./css/site.css", body: "body{}"},
			tarEntry{name: "./._index.html", body: "apple"},
			tarEntry{name: "./.DS_Store", body: "ds"},
			tarEntry{name: "./.git/HEAD", body: "ref"},
			tarEntry{name: "./.env", body: "SECRET=1"},
			tarEntry{name: "./link", kind: tar.TypeSymlink},
			tarEntry{name: "./css/site.css", body: "body{color:red}"},
		)
		rec, err := e.svc.PutLinkArchive(ctx, token, "10.1.1.1", body)
		if err != nil {
			t.Fatalf("gz=%v: %v", gz, err)
		}
		if rec.Files != 2 || rec.Upload.Files != 2 || len(rec.Skipped) != 2 ||
			rec.Skipped[0] != (SkippedFile{Path: ".env", Reason: SkipSensitive}) || rec.Skipped[1].Path != "link" {
			t.Fatalf("gz=%v receipt %+v", gz, rec)
		}
		data, err := os.ReadFile(filepath.Join(e.svc.uploads.sessions[info.ID].dir, "css", "site.css"))
		if err != nil || string(data) != "body{color:red}" {
			t.Fatalf("stored %q %v", data, err)
		}
		if _, err := e.svc.CommitUpload(ctx, u, info.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUploadLinkArchiveErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	site, files, fileBytes := int64(3000), int64(2), int64(2000)
	if _, err := e.svc.SetUserLimits(ctx, e.admin, u.User.ID, domain.LimitOverrides{
		MaxSiteBytes: &site, MaxFiles: &files, MaxFileBytes: &fileBytes,
	}); err != nil {
		t.Fatal(err)
	}
	info, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	token := linkToken(t, info)
	cases := []struct {
		body *bytes.Buffer
		code string
	}{
		{bytes.NewBufferString("not a tar at all, just some words long enough for a header"), domain.CodeArchiveCorrupt},
		{tarOf(t, false, tarEntry{name: "big.bin", body: strings.Repeat("a", 2001)}), domain.CodeArchiveFileTooBig},
		{tarOf(t, true, tarEntry{name: "a", body: strings.Repeat("a", 1600)}, tarEntry{name: "b", body: strings.Repeat("b", 1600)}), domain.CodeArchiveTooLarge},
		{tarOf(t, false, tarEntry{name: "a", body: "a"}, tarEntry{name: "b", body: "b"}, tarEntry{name: "c", body: "c"}), domain.CodeArchiveTooMany},
		{tarOf(t, false, tarEntry{name: "../escape", body: "x"}), domain.CodeInvalidInput},
		{tarOf(t, false, tarEntry{name: ".env", body: "x"}), domain.CodeInvalidInput},
	}
	for i, c := range cases {
		_, err := e.svc.PutLinkArchive(ctx, token, "10.1.1.1", c.body)
		if domain.AsError(err).Code != c.code {
			t.Errorf("case %d: %v, want %s", i, err, c.code)
		}
	}
	// A failed archive stores nothing.
	if n := len(e.svc.uploads.sessions[info.ID].files); n != 0 {
		t.Fatalf("partial archive stored %d files", n)
	}
	// The session total counts across requests.
	if _, err := e.svc.PutLinkFile(ctx, token, "10.1.1.1", "index.html", strings.NewReader(strings.Repeat("i", 2000))); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.PutLinkFile(ctx, token, "10.1.1.1", "b", strings.NewReader(strings.Repeat("b", 1001)))
	wantCode(t, err, domain.CodeArchiveTooLarge)
}

func TestUploadLinkExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	info, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	token := linkToken(t, info)
	e.clock.Advance(UploadLinkTTL - time.Minute)
	// Use extends the link.
	if _, err := e.svc.PutLinkFile(ctx, token, "10.1.1.1", "index.html", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(UploadLinkTTL - time.Minute)
	li, err := e.svc.UploadLinkInfo(ctx, token, "10.1.1.1")
	if err != nil || li.Files != 1 {
		t.Fatalf("info %+v %v", li, err)
	}
	// Reading the page does not extend it.
	e.clock.Advance(2 * time.Minute)
	_, err = e.svc.UploadLinkInfo(ctx, token, "10.1.1.1")
	wantCode(t, err, domain.CodeUploadLinkInvalid)
	// The session itself stays open for the assistant.
	if _, _, err := e.svc.UploadFiles(ctx, u, info.ID, []InlineFile{{Path: "b.txt", Content: "b"}}, nil); err != nil {
		t.Fatal(err)
	}
	// Never beyond the session: an idle session past its TTL kills the link.
	info2, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	sess := e.svc.uploads.sessions[info2.ID]
	sess.mu.Lock()
	sess.touched = e.clock.Now().Add(-UploadSessionTTL + time.Minute)
	sess.mu.Unlock()
	e.clock.Advance(2 * time.Minute)
	_, err = e.svc.UploadLinkInfo(ctx, linkToken(t, info2), "10.1.1.1")
	wantCode(t, err, domain.CodeUploadLinkInvalid)
}

func TestUploadLinkRejectsAndLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	info, _ := e.svc.BeginUpload(ctx, u, "", "", "", "")
	token := linkToken(t, info)
	for _, bad := range []string{"", "short", strings.Repeat("A", uploadLinkTokenLen), token[:42] + "!"} {
		_, err := e.svc.UploadLinkInfo(ctx, bad, "10.9.9.9")
		wantCode(t, err, domain.CodeUploadLinkInvalid)
	}
	// Failures are limited per network; then even a valid link waits.
	for range UploadLinkFailuresPerIP - 4 {
		_, _ = e.svc.UploadLinkInfo(ctx, "nope", "10.9.9.9")
	}
	_, err := e.svc.UploadLinkInfo(ctx, token, "10.9.9.9")
	wantCode(t, err, domain.CodeRateLimited)
	if _, err := e.svc.UploadLinkInfo(ctx, token, "10.1.1.1"); err != nil {
		t.Fatal(err)
	}
	// A blocked owner's link stops working.
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.UploadLinkInfo(ctx, token, "10.1.1.1")
	wantCode(t, err, domain.CodeUploadLinkInvalid)
}
