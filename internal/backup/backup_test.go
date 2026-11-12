package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/store"
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

func TestBackupRoundTripKeepsLinks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	state, sites := filepath.Join(dir, "state"), filepath.Join(dir, "sites")
	for _, d := range []string{state, filepath.Join(sites, "empty"), filepath.Join(sites, "sites", "abcdefghij", "releases", testDeploy), filepath.Join(sites, "by-host")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(sites, "sites", "abcdefghij", "releases", testDeploy, "index.html"), []byte("hi"), 0o644)
	_ = os.Symlink("releases/"+testDeploy, filepath.Join(sites, "sites", "abcdefghij", "current"))
	_ = os.Symlink("../sites/abcdefghij/current", filepath.Join(sites, "by-host", "a.pp.test"))
	db, err := store.Open(ctx, filepath.Join(state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	m, err := Backup(ctx, db, sites, state, &buf)
	_ = db.Close()
	if err != nil || m.SchemaVersion != store.LatestVersion() {
		t.Fatalf("backup: %v %+v", err, m)
	}

	out := t.TempDir()
	state2, sites2 := filepath.Join(out, "state"), filepath.Join(out, "sites")
	_ = os.MkdirAll(state2, 0o755)
	if _, err := Restore(ctx, bytes.NewReader(buf.Bytes()), state2, sites2, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(sites2, "by-host", "a.pp.test", "index.html"))
	if err != nil || string(b) != "hi" {
		t.Fatalf("served through restored links: %q %v", b, err)
	}
	if _, err := Restore(ctx, bytes.NewReader(buf.Bytes()), state2, sites2, false); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("second restore without force: %v", err)
	}
	if _, err := Restore(ctx, bytes.NewReader(buf.Bytes()), state2, sites2, true); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	db2, err := store.Open(ctx, filepath.Join(state2, "state.db"))
	if err != nil {
		t.Fatalf("restored database opens: %v", err)
	}
	_ = db2.Close()
}

func TestMoveAcrossFilesystemsFallback(t *testing.T) {
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "a"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "a", "f"), []byte("x"), 0o644)
	_ = os.Symlink("a/f", filepath.Join(src, "l"))
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTreeWithLinks(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "l")); string(b) != "x" {
		t.Fatal("copy must keep links")
	}
}
