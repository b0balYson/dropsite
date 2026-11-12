package archive

import (
	"bufio"
	"context"
	"errors"
	"os"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Extract validates src (zip or tar.gz, detected by magic bytes, not by name) and
// extracts it into dest, which must not exist yet (create it with 0755).
// On any error dest is removed completely and a *domain.Error is returned.
// If dest already exists it is left untouched and an internal error is returned.
func Extract(ctx context.Context, src, dest string, lim Limits, now time.Time) (Result, error) {
	if err := os.Mkdir(dest, dirPerm); err != nil {
		return Result{}, internalErr("create destination directory", err)
	}
	res, err := extractInto(ctx, src, dest, lim, now)
	if err == nil {
		return res, nil
	}
	de := domain.AsError(err)
	if rmErr := os.RemoveAll(dest); rmErr != nil {
		de = de.WithCause(errors.Join(de.Unwrap(), rmErr))
	}
	return Result{}, de
}

func extractInto(ctx context.Context, src, dest string, lim Limits, now time.Time) (Result, error) {
	if err := ctxErr(ctx); err != nil {
		return Result{}, err
	}
	f, err := os.Open(src) //nolint:gosec // src is a server-side temp file chosen by the caller.
	if err != nil {
		return Result{}, internalErr("open archive", err)
	}
	defer func() { _ = f.Close() }()
	root, err := os.OpenRoot(dest)
	if err != nil {
		return Result{}, internalErr("open destination", err)
	}
	defer func() { _ = root.Close() }()

	w, t := newWriter(root, lim), newTree(lim)
	if err := unpack(ctx, f, w, t); err != nil {
		return Result{}, err
	}
	if t.files == 0 {
		return Result{}, fail(domain.CodeArchiveEmpty, "archive contains no files", nil)
	}
	if err := finish(root, now); err != nil {
		return Result{}, err
	}
	return Result{Files: t.files, Bytes: w.budget.bytes}, nil
}

func unpack(ctx context.Context, f *os.File, w *writer, t *tree) error {
	kind, err := detect(f)
	if err != nil {
		return err
	}
	if kind == formatZip {
		st, err := f.Stat()
		if err != nil {
			return internalErr("stat archive", err)
		}
		return extractZip(ctx, f, st.Size(), w, t)
	}
	// detect uses ReadAt, so the file offset is still at the start.
	return extractTarGz(ctx, bufio.NewReaderSize(f, copyBufSize), w, t)
}

// finish lifts a single wrapper directory, requires index.html at the root and
// normalizes permissions and times of the whole tree.
func finish(root *os.Root, now time.Time) error {
	if err := hoist(root); err != nil {
		return err
	}
	if err := requireIndex(root); err != nil {
		return err
	}
	return normalize(root, now)
}
