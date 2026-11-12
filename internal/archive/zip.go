package archive

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
)

type zipItem struct {
	file *zip.File
	rel  string
	dir  bool
}

// extractZip first scans the whole central directory (names, types, counts)
// so violations are rejected before anything is written, then extracts.
func extractZip(ctx context.Context, r io.ReaderAt, size int64, w *writer, t *tree) error {
	zr, err := zip.NewReader(r, size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return corruptErr(err)
	}
	plan, err := scanZip(ctx, zr.File, t)
	if err != nil {
		return err
	}
	for _, it := range plan {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if err := extractZipItem(ctx, it, w); err != nil {
			return err
		}
	}
	return nil
}

func scanZip(ctx context.Context, files []*zip.File, t *tree) ([]zipItem, error) {
	plan := make([]zipItem, 0, len(files))
	for _, f := range files {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		typ := zipType(f.Mode())
		rel, skip, err := t.admit(f.Name, typ)
		if err != nil {
			return nil, err
		}
		if !skip {
			plan = append(plan, zipItem{file: f, rel: rel, dir: typ == typeDir})
		}
	}
	return plan, nil
}

func extractZipItem(ctx context.Context, it zipItem, w *writer) error {
	if it.dir {
		return w.mkdirAll(it.rel)
	}
	rc, err := it.file.Open()
	if err != nil {
		return corruptErr(err)
	}
	defer func() { _ = rc.Close() }()
	return w.writeFile(ctx, it.rel, it.file.Name, rc)
}

// zipType classifies an entry by the mode stored in its external attributes.
func zipType(m fs.FileMode) entryType {
	switch {
	case m&fs.ModeSymlink != 0:
		return typeLink
	case m.IsDir():
		return typeDir
	case m&fs.ModeType != 0:
		return typeSpecial
	}
	return typeFile
}
