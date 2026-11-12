package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveOf(t *testing.T, add func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	m, _ := json.Marshal(Manifest{Format: formatV1, SchemaVersion: 1})
	_ = tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(m)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(m)
	_ = tw.WriteHeader(&tar.Header{Name: dbName, Mode: 0o600, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("db"))
	add(tw)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestRestoreRejectsUnsafeEntries(t *testing.T) {
	cases := map[string]func(tw *tar.Writer){
		"traversal": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "../escape", Typeflag: tar.TypeReg, Mode: 0o644})
		},
		"absolute link": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "by-host/x", Linkname: "/etc", Typeflag: tar.TypeSymlink})
		},
		"escaping link": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "by-host/x", Linkname: "../../../etc", Typeflag: tar.TypeSymlink})
		},
		"foreign entry": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "other/file", Typeflag: tar.TypeReg, Mode: 0o644})
		},
		"device": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "dev", Typeflag: tar.TypeChar})
		},
	}
	for name, add := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			state, sites := filepath.Join(dir, "state"), filepath.Join(dir, "sites")
			_ = os.MkdirAll(state, 0o755)
			_, err := Restore(context.Background(), bytes.NewReader(archiveOf(t, add)), state, sites, true)
			if err == nil {
				t.Fatal("must be rejected")
			}
			if _, err := os.Stat(filepath.Join(state, "state.db")); !os.IsNotExist(err) {
				t.Fatal("nothing may be installed from a rejected archive")
			}
		})
	}
}

func TestRestoreAcceptsInternalLinks(t *testing.T) {
	dir := t.TempDir()
	state, sites := filepath.Join(dir, "state"), filepath.Join(dir, "sites")
	_ = os.MkdirAll(state, 0o755)
	data := archiveOf(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "empty/", Typeflag: tar.TypeDir, Mode: 0o755})
		_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "sites/abcdefghij/current", Linkname: "../../empty", Typeflag: tar.TypeSymlink})
		_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + "by-host/a.pp.test", Linkname: "../sites/abcdefghij/current", Typeflag: tar.TypeSymlink})
	})
	if _, err := Restore(context.Background(), bytes.NewReader(data), state, sites, false); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(sites, "by-host", "a.pp.test"))
	if err != nil || target != "../sites/abcdefghij/current" {
		t.Fatalf("link not restored: %q %v", target, err)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "state.db")); !strings.EqualFold(string(b), "db") {
		t.Fatal("database not installed")
	}
}

func TestLinkChecks(t *testing.T) {
	ok := map[string]string{"by-host/a": "../sites/x/current", "sites/x/current": "releases/1"}
	for rel, target := range ok {
		if !linkTargetInside(rel, target) {
			t.Errorf("%s -> %s should be inside", rel, target)
		}
	}
	bad := map[string]string{"by-host/a": "../../x", "a": "/etc", "b": ""}
	for rel, target := range bad {
		if linkTargetInside(rel, target) {
			t.Errorf("%s -> %s should be outside", rel, target)
		}
	}
	if isInside("../x") || isInside("") || !isInside("a/b") {
		t.Fatal("isInside")
	}
}
