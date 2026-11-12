package secrets

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
)

const (
	// MaxFileBytes is how much of one file is scanned.
	MaxFileBytes = 8 << 20
	// sniffBytes is the prefix checked for NUL bytes to skip binary files.
	sniffBytes = 8000
)

func scanDir(ctx context.Context, dir string) ([]Finding, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	var out []Finding
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := readPrefix(root, p)
		if err != nil {
			return err
		}
		if binary(data) {
			return nil
		}
		out = append(out, ScanText(p, data)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan for secrets: %w", err)
	}
	sortFindings(out)
	return out, nil
}

func readPrefix(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, MaxFileBytes))
}

func binary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), sniffBytes)], 0) >= 0
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Path != fs[j].Path {
			return fs[i].Path < fs[j].Path
		}
		return fs[i].Line < fs[j].Line
	})
}
