//go:build unix

package archive

import (
	"bytes"
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// TestExtractIgnoresUmask must not run in parallel: umask is process-wide.
func TestExtractIgnoresUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	src := makeTarGz(t,
		tent{name: "index.html", body: page},
		tent{name: "a/b/c.txt", body: "c"})
	_, dest := sandbox(t)
	if _, err := Extract(context.Background(), src, dest, bigLimits, testNow); err != nil {
		t.Fatal(err)
	}
	assertNormalized(t, dest)
}

func TestExtractNormalizesArchiveModes(t *testing.T) {
	src := makeZip(t,
		zent{name: "index.html", body: page, mode: 0o4777},
		zent{name: "bin/", mode: fs.ModeDir | 0o700},
		zent{name: "bin/run.sh", body: "#!/bin/sh", mode: 0o755})
	_, dest := sandbox(t)
	if _, err := Extract(context.Background(), src, dest, bigLimits, testNow); err != nil {
		t.Fatal(err)
	}
	assertNormalized(t, dest)
}

func TestWriteTarGzRejectsFIFO(t *testing.T) {
	dir := mkTree(t, map[string]string{"index.html": page})
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var buf bytes.Buffer
	_, err := WriteTarGz(context.Background(), dir, &buf, bigLimits)
	de := wantCode(t, err, domain.CodeArchiveSpecialFile)
	if de.Params["path"] != "pipe" {
		t.Fatalf("params = %v", de.Params)
	}
}
