package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

const page = "<h1>hi</h1>"

func TestExtractRejects(t *testing.T) {
	index := zent{name: "index.html", body: page}
	tindex := tent{name: "index.html", body: page}
	zeros := strings.Repeat("\x00", 8<<20)
	cases := []struct {
		name  string
		src   func(t *testing.T) string
		lim   Limits
		codes []string
		path  string
	}{
		{"zip slip", func(t *testing.T) string { return makeZip(t, index, zent{name: "../../etc/passwd", body: "x"}) }, bigLimits, []string{domain.CodeArchiveTraversal}, "../../etc/passwd"},
		{"zip slip nested", func(t *testing.T) string { return makeZip(t, index, zent{name: "a/../../x", body: "x"}) }, bigLimits, []string{domain.CodeArchiveTraversal}, "a/../../x"},
		{"tar slip", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "../../x", body: "x"}) }, bigLimits, []string{domain.CodeArchiveTraversal}, "../../x"},
		{"slip inside ignored dir", func(t *testing.T) string { return makeZip(t, index, zent{name: "node_modules/../../x", body: "x"}) }, bigLimits, []string{domain.CodeArchiveTraversal}, ""},
		{"zip absolute", func(t *testing.T) string { return makeZip(t, index, zent{name: "/etc/x", body: "x"}) }, bigLimits, []string{domain.CodeArchiveAbsPath}, "/etc/x"},
		{"tar absolute", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "/tmp/x", body: "x"}) }, bigLimits, []string{domain.CodeArchiveAbsPath}, "/tmp/x"},
		{"windows drive", func(t *testing.T) string { return makeZip(t, index, zent{name: `C:\x`, body: "x"}) }, bigLimits, []string{domain.CodeArchiveAbsPath}, ""},
		{"unc path", func(t *testing.T) string { return makeZip(t, index, zent{name: `\\srv\x`, body: "x"}) }, bigLimits, []string{domain.CodeArchiveAbsPath}, ""},
		{"backslash", func(t *testing.T) string { return makeZip(t, index, zent{name: `a\..\..\x`, body: "x"}) }, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"nul byte", func(t *testing.T) string { return makeZip(t, index, zent{name: "a\x00b", body: "x"}) }, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"control char", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "a\x1bb", body: "x"}) }, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"invalid utf8", func(t *testing.T) string { return makeZip(t, index, zent{name: "a\xffb", body: "x"}) }, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"empty segment", func(t *testing.T) string { return makeZip(t, index, zent{name: "a//b", body: "x"}) }, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"long segment", func(t *testing.T) string { return makeZip(t, index, zent{name: strings.Repeat("a", 256), body: "x"}) }, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"dot file", func(t *testing.T) string {
			return makeTarGz(t, tindex, tent{name: "./", typ: tar.TypeDir}, tent{name: ".", body: "x"})
		}, bigLimits, []string{domain.CodeArchiveBadName}, ""},
		{"zip symlink", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "evil", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777})
		}, bigLimits, []string{domain.CodeArchiveLink}, "evil"},
		{"tar symlink", func(t *testing.T) string {
			return makeTarGz(t, tindex, tent{name: "evil", typ: tar.TypeSymlink, link: "/etc/passwd"})
		}, bigLimits, []string{domain.CodeArchiveLink}, "evil"},
		{"tar hard link", func(t *testing.T) string {
			return makeTarGz(t, tindex, tent{name: "evil", typ: tar.TypeLink, link: "index.html"})
		}, bigLimits, []string{domain.CodeArchiveLink}, "evil"},
		{"tar char device", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "dev", typ: tar.TypeChar}) }, bigLimits, []string{domain.CodeArchiveSpecialFile}, "dev"},
		{"tar block device", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "dev", typ: tar.TypeBlock}) }, bigLimits, []string{domain.CodeArchiveSpecialFile}, "dev"},
		{"tar fifo", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "pipe", typ: tar.TypeFifo}) }, bigLimits, []string{domain.CodeArchiveSpecialFile}, "pipe"},
		{"zip fifo", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "pipe", mode: fs.ModeNamedPipe | 0o644})
		}, bigLimits, []string{domain.CodeArchiveSpecialFile}, "pipe"},
		{"zip device", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "dev", mode: fs.ModeDevice | fs.ModeCharDevice | 0o644})
		}, bigLimits, []string{domain.CodeArchiveSpecialFile}, "dev"},
		{"duplicate file", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "a.css", body: "1"}, zent{name: "./a.css", body: "2"})
		}, bigLimits, []string{domain.CodeArchiveBadName}, "./a.css"},
		{"tar duplicate", func(t *testing.T) string { return makeTarGz(t, tindex, tindex) }, bigLimits, []string{domain.CodeArchiveBadName}, "index.html"},
		{"file then dir", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "a", body: "1"}, zent{name: "a/b", body: "2"})
		}, bigLimits, []string{domain.CodeArchiveBadName}, "a/b"},
		{"dir then file", func(t *testing.T) string {
			return makeTarGz(t, tindex, tent{name: "a/b", body: "1"}, tent{name: "a", body: "2"})
		}, bigLimits, []string{domain.CodeArchiveBadName}, "a"},
		{"duplicate dir", func(t *testing.T) string { return makeZip(t, index, zent{name: "a/"}, zent{name: "a/"}) }, bigLimits, []string{domain.CodeArchiveBadName}, "a/"},
		{"too many files", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "a", body: "1"}, zent{name: "b", body: "2"})
		}, Limits{MaxFiles: 2}, []string{domain.CodeArchiveTooMany}, ""},
		{"tar too many files", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "a", body: "1"}) }, Limits{MaxFiles: 1}, []string{domain.CodeArchiveTooMany}, ""},
		{"file too big", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "big.js", body: strings.Repeat("x", 101)})
		}, Limits{MaxFileBytes: 100}, []string{domain.CodeArchiveFileTooBig}, "big.js"},
		{"tar file too big", func(t *testing.T) string {
			return makeTarGz(t, tindex, tent{name: "big.js", body: strings.Repeat("x", 101)})
		}, Limits{MaxFileBytes: 100}, []string{domain.CodeArchiveFileTooBig}, "big.js"},
		{"total too large", func(t *testing.T) string {
			return makeZip(t, index, zent{name: "a", body: strings.Repeat("x", 60)}, zent{name: "b", body: strings.Repeat("x", 60)})
		}, Limits{MaxTotalBytes: 100}, []string{domain.CodeArchiveTooLarge}, ""},
		{"zip bomb per file", func(t *testing.T) string { return makeZip(t, index, zent{name: "z.bin", body: zeros}) }, Limits{MaxFileBytes: 1 << 20}, []string{domain.CodeArchiveFileTooBig}, "z.bin"},
		{"zip bomb total", func(t *testing.T) string { return makeZip(t, index, zent{name: "z.bin", body: zeros}) }, Limits{MaxTotalBytes: 1 << 20}, []string{domain.CodeArchiveTooLarge}, ""},
		{"tar.gz bomb", func(t *testing.T) string { return makeTarGz(t, tindex, tent{name: "z.bin", body: zeros}) }, Limits{MaxTotalBytes: 1 << 20}, []string{domain.CodeArchiveTooLarge}, ""},
		{"lying zip size", func(t *testing.T) string { return lyingZip(t, []byte(zeros), 10) }, Limits{MaxFileBytes: 1 << 20}, []string{domain.CodeArchiveCorrupt, domain.CodeArchiveFileTooBig}, ""},
		{"no index", func(t *testing.T) string { return makeZip(t, zent{name: "about.html", body: page}) }, bigLimits, []string{domain.CodeArchiveNoIndex}, ""},
		{"index is a dir", func(t *testing.T) string {
			return makeZip(t, zent{name: "index.html/a", body: page}, zent{name: "b", body: "x"})
		}, bigLimits, []string{domain.CodeArchiveNoIndex}, ""},
		{"two top dirs", func(t *testing.T) string {
			return makeZip(t, zent{name: "a/index.html", body: page}, zent{name: "b/index.html", body: page})
		}, bigLimits, []string{domain.CodeArchiveNoIndex}, ""},
		{"wrapper two levels", func(t *testing.T) string { return makeZip(t, zent{name: "a/b/index.html", body: page}) }, bigLimits, []string{domain.CodeArchiveNoIndex}, ""},
		{"empty zip", func(t *testing.T) string { return makeZip(t) }, bigLimits, []string{domain.CodeArchiveEmpty}, ""},
		{"only dirs", func(t *testing.T) string { return makeTarGz(t, tent{name: "a/", typ: tar.TypeDir}) }, bigLimits, []string{domain.CodeArchiveEmpty}, ""},
		{"only ignored", func(t *testing.T) string {
			return makeZip(t, zent{name: "__MACOSX/._index.html", body: "x"}, zent{name: ".DS_Store", body: "x"})
		}, bigLimits, []string{domain.CodeArchiveEmpty}, ""},
		{"unknown format", func(t *testing.T) string { return writeFile(t, []byte("<html>not an archive</html>")) }, bigLimits, []string{domain.CodeArchiveFormat}, ""},
		{"empty file", func(t *testing.T) string { return writeFile(t, nil) }, bigLimits, []string{domain.CodeArchiveFormat}, ""},
		{"zip named tar", func(t *testing.T) string { return writeFile(t, []byte("PK\x07\x08rest")) }, bigLimits, []string{domain.CodeArchiveFormat}, ""},
		{"broken gzip", func(t *testing.T) string { return writeFile(t, []byte("\x1f\x8bgarbage-garbage-garbage")) }, bigLimits, []string{domain.CodeArchiveCorrupt}, ""},
		{"broken zip", func(t *testing.T) string { return writeFile(t, []byte("PK\x03\x04garbage-garbage-garbage")) }, bigLimits, []string{domain.CodeArchiveCorrupt}, ""},
		{"gzip without tar", func(t *testing.T) string { return writeFile(t, gzipOf(t, []byte(strings.Repeat("hello ", 200)))) }, bigLimits, []string{domain.CodeArchiveCorrupt}, ""},
		{"truncated tar.gz", func(t *testing.T) string {
			b := tarGzBytes(t, []tent{tindex, {name: "x.bin", body: strings.Repeat("ab", 50000)}})
			return writeFile(t, b[:len(b)/2])
		}, bigLimits, []string{domain.CodeArchiveCorrupt}, ""},
		{"tar.gz trailing data", func(t *testing.T) string {
			b := tarGzBytes(t, []tent{tindex})
			return writeFile(t, append(b, gzipOf(t, make([]byte, maxTrailer+10))...))
		}, bigLimits, []string{domain.CodeArchiveCorrupt}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, dest := sandbox(t)
			before := listTree(t, base)
			res, err := Extract(context.Background(), tc.src(t), dest, tc.lim, testNow)
			de := wantCode(t, err, tc.codes...)
			if tc.path != "" && de.Params["path"] != tc.path {
				t.Errorf("params = %v, want path %q", de.Params, tc.path)
			}
			if res != (Result{}) {
				t.Errorf("result on error = %+v", res)
			}
			if _, err := os.Lstat(dest); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("dest must not exist after error, stat err = %v", err)
			}
			if after := listTree(t, base); !reflect.DeepEqual(before, after) {
				t.Errorf("tree outside dest changed: %v -> %v", before, after)
			}
		})
	}
}

func gzipOf(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractLimitParams(t *testing.T) {
	_, dest := sandbox(t)
	src := makeZip(t, zent{name: "index.html", body: page}, zent{name: "a", body: "1"})
	_, err := Extract(context.Background(), src, dest, Limits{MaxFiles: 1}, testNow)
	de := wantCode(t, err, domain.CodeArchiveTooMany)
	if de.Params["limit"] != 1 || de.Kind != domain.KindTooLarge {
		t.Fatalf("params %v kind %v", de.Params, de.Kind)
	}
}

func TestExtractSuccess(t *testing.T) {
	cases := []struct {
		name string
		src  func(t *testing.T) string
		lim  Limits
		want map[string]string
	}{
		{"flat zip", func(t *testing.T) string {
			return makeZip(t, zent{name: "index.html", body: page}, zent{name: "css/app.css", body: "body{}"}, zent{name: "img/", mode: fs.ModeDir | 0o700})
		}, bigLimits, map[string]string{"index.html": page, "css/": "", "css/app.css": "body{}", "img/": ""}},
		{"zip wrapper with junk", func(t *testing.T) string {
			return makeZip(t,
				zent{name: "dist/", mode: fs.ModeDir | 0o755},
				zent{name: "dist/index.html", body: page},
				zent{name: "dist/js/a.js", body: "1"},
				zent{name: "__MACOSX/dist/._index.html", body: "junk"},
				zent{name: ".DS_Store", body: "junk"},
				zent{name: "dist/.DS_Store", body: "junk"})
		}, Limits{MaxFiles: 2}, map[string]string{"index.html": page, "js/": "", "js/a.js": "1"}},
		{"tar wrapper", func(t *testing.T) string {
			return makeTarGz(t, tent{name: "./", typ: tar.TypeDir}, tent{name: "./dist/", typ: tar.TypeDir},
				tent{name: "./dist/index.html", body: page}, tent{name: "./Thumbs.db", body: "x"})
		}, bigLimits, map[string]string{"index.html": page}},
		{"wrapper containing same name", func(t *testing.T) string {
			return makeZip(t, zent{name: "dist/index.html", body: page}, zent{name: "dist/dist/x.txt", body: "x"}, zent{name: "dist/.hoist", body: "h"})
		}, bigLimits, map[string]string{"index.html": page, "dist/": "", "dist/x.txt": "x", ".hoist": "h"}},
		{"root index with one dir is not lifted", func(t *testing.T) string {
			return makeZip(t, zent{name: "index.html", body: page}, zent{name: "dist/index.html", body: "inner"})
		}, bigLimits, map[string]string{"index.html": page, "dist/": "", "dist/index.html": "inner"}},
		{"ignored entries skip types and limits", func(t *testing.T) string {
			return makeTarGz(t, tent{name: "index.html", body: page},
				tent{name: "node_modules/.bin/x", typ: tar.TypeSymlink, link: "/bin/sh"},
				tent{name: ".git/config", body: strings.Repeat("x", 500)},
				tent{name: "sub/.git/HEAD", body: "ref"},
				tent{name: ".dropsite.json", body: "{}"},
				tent{name: "sub/.dropsite.json", body: "{}"})
		}, Limits{MaxFiles: 2, MaxTotalBytes: 100}, map[string]string{"index.html": page, "sub/": "", "sub/.dropsite.json": "{}"}},
		{"unicode names", func(t *testing.T) string {
			return makeZip(t, zent{name: "index.html", body: page}, zent{name: "Картинки/фото 1.png", body: "p"})
		}, bigLimits, map[string]string{"index.html": page, "Картинки/": "", "Картинки/фото 1.png": "p"}},
		{"implicit dir then explicit", func(t *testing.T) string {
			return makeZip(t, zent{name: "index.html", body: page}, zent{name: "a/b.txt", body: "b"}, zent{name: "a/", mode: fs.ModeDir | 0o755})
		}, bigLimits, map[string]string{"index.html": page, "a/": "", "a/b.txt": "b"}},
		{"global pax header", func(t *testing.T) string {
			return makeTarGz(t, tent{name: "pax_global_header", typ: tar.TypeXGlobalHeader}, tent{name: "index.html", body: page})
		}, bigLimits, map[string]string{"index.html": page}},
		{"unlimited", func(t *testing.T) string { return makeZip(t, zent{name: "index.html", body: page}) }, Limits{}, map[string]string{"index.html": page}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dest := sandbox(t)
			res, err := Extract(context.Background(), tc.src(t), dest, tc.lim, testNow)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			got := readTree(t, dest)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("tree = %v, want %v", got, tc.want)
			}
			files, size := countFiles(tc.want)
			if res.Files != files || res.Bytes != size {
				t.Fatalf("result = %+v, want files=%d bytes=%d", res, files, size)
			}
			assertNormalized(t, dest)
		})
	}
}

func countFiles(m map[string]string) (int, int64) {
	var n int
	var size int64
	for k, v := range m {
		if !strings.HasSuffix(k, "/") {
			n++
			size += int64(len(v))
		}
	}
	return n, size
}

func assertNormalized(t *testing.T, dest string) {
	t.Helper()
	paths := append([]string{"."}, listTree(t, dest)...)
	for _, rel := range paths {
		fi, err := os.Lstat(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		want := filePerm
		if fi.IsDir() {
			want = dirPerm | fs.ModeDir
		}
		if fi.Mode() != want {
			t.Errorf("%s mode = %v, want %v", rel, fi.Mode(), want)
		}
		if !fi.ModTime().Equal(testNow) {
			t.Errorf("%s mtime = %v, want %v", rel, fi.ModTime(), testNow)
		}
	}
}

func TestExtractDestExists(t *testing.T) {
	dest := t.TempDir()
	marker := filepath.Join(dest, "keep")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Extract(context.Background(), makeZip(t, zent{name: "index.html", body: page}), dest, bigLimits, testNow)
	_ = wantCode(t, err, domain.CodeInternal)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("pre-existing destination must be left untouched")
	}
}

func TestExtractMissingSource(t *testing.T) {
	_, dest := sandbox(t)
	_, err := Extract(context.Background(), filepath.Join(t.TempDir(), "nope"), dest, bigLimits, testNow)
	_ = wantCode(t, err, domain.CodeInternal)
	if _, err := os.Lstat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("dest must be removed")
	}
}

func TestExtractCancelled(t *testing.T) {
	for _, name := range []string{"zip", "tar.gz"} {
		t.Run(name, func(t *testing.T) {
			src := makeZip(t, zent{name: "index.html", body: page})
			if name == "tar.gz" {
				src = makeTarGz(t, tent{name: "index.html", body: page})
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, dest := sandbox(t)
			_, err := Extract(ctx, src, dest, bigLimits, testNow)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled, got %v", err)
			}
			_ = wantCode(t, err, domain.CodeInternal)
			if _, err := os.Lstat(dest); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("dest must be removed")
			}
		})
	}
}

// cancelAfterRead cancels the context after the first chunk, imitating a
// cancellation in the middle of a large file.
type cancelAfterRead struct {
	cancel context.CancelFunc
	n      int
}

func (c *cancelAfterRead) Read(p []byte) (int, error) {
	c.n++
	if c.n > 1 {
		c.cancel()
	}
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestCopyCancelledMidFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newBudget(Limits{})
	var sink bytes.Buffer
	err := b.copy(ctx, &sink, &cancelAfterRead{cancel: cancel}, "big")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if sink.Len() > 2*copyBufSize {
		t.Fatalf("copy kept going after cancellation: %d bytes", sink.Len())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestCopyWriteError(t *testing.T) {
	err := newBudget(Limits{}).copy(context.Background(), failWriter{}, strings.NewReader("abc"), "a")
	_ = wantCode(t, err, domain.CodeInternal)
}
