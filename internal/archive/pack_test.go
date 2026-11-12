package archive

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func mkTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWriteTarGzRoundTrip(t *testing.T) {
	dir := mkTree(t, map[string]string{
		"index.html":           page,
		"css/app.css":          "body{}",
		"Картинки/фото.png":    "png",
		".dropsite.json":       `{"site":"x"}`,
		".git/HEAD":            "ref",
		"node_modules/x/a.js":  "x",
		"sub/.DS_Store":        "junk",
		"sub/.dropsite.json":   "kept",
		"deep/a/b/c/d/e/f.txt": "deep",
	})
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res, err := WriteTarGz(context.Background(), dir, &buf, bigLimits)
	if err != nil {
		t.Fatalf("WriteTarGz: %v", err)
	}
	src := writeFile(t, buf.Bytes())
	_, dest := sandbox(t)
	got, err := Extract(context.Background(), src, dest, bigLimits, testNow)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got != res {
		t.Fatalf("pack result %+v != extract result %+v", res, got)
	}
	want := map[string]string{
		"index.html": page, "css/": "", "css/app.css": "body{}",
		"Картинки/": "", "Картинки/фото.png": "png",
		"sub/": "", "sub/.dropsite.json": "kept", "empty/": "",
		"deep/": "", "deep/a/": "", "deep/a/b/": "", "deep/a/b/c/": "", "deep/a/b/c/d/": "", "deep/a/b/c/d/e/": "", "deep/a/b/c/d/e/f.txt": "deep",
	}
	if tree := readTree(t, dest); !reflect.DeepEqual(tree, want) {
		t.Fatalf("tree = %v\nwant %v", tree, want)
	}
}

func TestWriteTarGzRejects(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		lim   Limits
		code  string
		path  string
	}{
		{"symlink", func(t *testing.T) string {
			dir := mkTree(t, map[string]string{"index.html": page, "a/x.txt": "x"})
			symlink(t, "/etc/passwd", filepath.Join(dir, "a", "link"))
			return dir
		}, bigLimits, domain.CodeArchiveLink, "a/link"},
		{"symlinked dir", func(t *testing.T) string {
			dir := mkTree(t, map[string]string{"index.html": page})
			symlink(t, t.TempDir(), filepath.Join(dir, "assets"))
			return dir
		}, bigLimits, domain.CodeArchiveLink, "assets"},
		{"no index", func(t *testing.T) string { return mkTree(t, map[string]string{"about.html": page}) }, bigLimits, domain.CodeArchiveNoIndex, ""},
		{"index symlink", func(t *testing.T) string {
			dir := mkTree(t, map[string]string{"real.html": page})
			symlink(t, "real.html", filepath.Join(dir, "index.html"))
			return dir
		}, bigLimits, domain.CodeArchiveNoIndex, ""},
		{"bad name", func(t *testing.T) string { return mkTree(t, map[string]string{"index.html": page, `a\b.txt`: "x"}) }, bigLimits, domain.CodeArchiveBadName, `a\b.txt`},
		{"too many", func(t *testing.T) string { return mkTree(t, map[string]string{"index.html": page, "a": "1"}) }, Limits{MaxFiles: 1}, domain.CodeArchiveTooMany, ""},
		{"file too big", func(t *testing.T) string { return mkTree(t, map[string]string{"index.html": page, "a": "0123456789"}) }, Limits{MaxFileBytes: 5}, domain.CodeArchiveFileTooBig, "a"},
		{"total too large", func(t *testing.T) string { return mkTree(t, map[string]string{"index.html": page, "a": "0123456789"}) }, Limits{MaxTotalBytes: 15}, domain.CodeArchiveTooLarge, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			_, err := WriteTarGz(context.Background(), tc.setup(t), &buf, tc.lim)
			de := wantCode(t, err, tc.code)
			if tc.path != "" && de.Params["path"] != tc.path {
				t.Errorf("params = %v, want path %q", de.Params, tc.path)
			}
			if buf.Len() != 0 {
				t.Errorf("nothing must be written on validation errors, got %d bytes", buf.Len())
			}
		})
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
}

func TestWriteTarGzMissingDir(t *testing.T) {
	_, err := WriteTarGz(context.Background(), filepath.Join(t.TempDir(), "nope"), &bytes.Buffer{}, bigLimits)
	_ = wantCode(t, err, domain.CodeInternal)
}

func TestWriteTarGzCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := mkTree(t, map[string]string{"index.html": page})
	_, err := WriteTarGz(ctx, dir, &bytes.Buffer{}, bigLimits)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestWriteTarGzWriterError(t *testing.T) {
	dir := mkTree(t, map[string]string{"index.html": page})
	_, err := WriteTarGz(context.Background(), dir, failWriter{}, bigLimits)
	_ = wantCode(t, err, domain.CodeInternal)
}
