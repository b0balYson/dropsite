package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

var testNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

var bigLimits = Limits{MaxTotalBytes: 50 << 20, MaxFileBytes: 20 << 20, MaxFiles: 2000}

type zent struct {
	name string
	body string
	mode fs.FileMode
}

type tent struct {
	name string
	body string
	typ  byte
	link string
}

func writeFile(t testing.TB, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func zipBytes(t testing.TB, ents []zent) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, ents ...zent) string {
	t.Helper()
	return writeFile(t, zipBytes(t, ents))
}

func tarGzBytes(t *testing.T, ents []tent) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o600, Typeflag: typ, Linkname: e.link, ModTime: time.Unix(1, 0)}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if typ == tar.TypeXGlobalHeader {
			hdr = &tar.Header{Typeflag: typ, PAXRecords: map[string]string{"comment": "x"}}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTarGz(t *testing.T, ents ...tent) string {
	t.Helper()
	return writeFile(t, tarGzBytes(t, ents))
}

// lyingZip stores a deflated body whose header claims a smaller size.
func lyingZip(t *testing.T, body []byte, claimed uint64) string {
	t.Helper()
	var comp bytes.Buffer
	fw, err := flate.NewWriter(&comp, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "index.html", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(body),
		CompressedSize64: uint64(comp.Len()), UncompressedSize64: claimed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(comp.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, buf.Bytes())
}

// sandbox returns a base directory and a destination two levels below it, so
// that "../../x" would land inside base and be observable.
func sandbox(t *testing.T) (base, dest string) {
	t.Helper()
	base = t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	return base, filepath.Join(base, "a", "b", "out")
}

// listTree returns every path below dir, slash-separated and sorted.
func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func wantCode(t *testing.T, err error, codes ...string) *domain.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %v, got nil", codes)
	}
	var de *domain.Error
	if !asDomain(err, &de) {
		t.Fatalf("want *domain.Error, got %T: %v", err, err)
	}
	for _, c := range codes {
		if de.Code == c {
			return de
		}
	}
	t.Fatalf("want code %v, got %v", codes, err)
	return nil
}

func asDomain(err error, target **domain.Error) bool {
	de, ok := err.(*domain.Error) //nolint:errorlint // the API promises a bare *domain.Error.
	*target = de
	return ok
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range listTree(t, dir) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.IsDir() {
			out[rel+"/"] = ""
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[rel] = string(b)
	}
	return out
}
