package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

type packEntry struct {
	rel   string
	dir   bool
	size  int64
	mtime time.Time
}

// WriteTarGz packs dir into a gzip-compressed tar stream for the CLI: same IsIgnored
// rules, regular files and directories only (a symlink or special file → *domain.Error
// with CodeArchiveLink / CodeArchiveSpecialFile naming the path), requires index.html
// at the root (CodeArchiveNoIndex), enforces lim, honours ctx cancellation.
// The whole tree is validated before the first byte is written to w.
func WriteTarGz(ctx context.Context, dir string, w io.Writer, lim Limits) (Result, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Result{}, internalErr("open directory", err)
	}
	defer func() { _ = root.Close() }()
	if err := requireIndex(root); err != nil {
		return Result{}, err
	}
	s := &packScan{ctx: ctx, lim: lim}
	if err := fs.WalkDir(root.FS(), ".", s.visit); err != nil {
		return Result{}, domain.AsError(err)
	}
	if err := writeEntries(ctx, root, s.entries, w); err != nil {
		return Result{}, domain.AsError(err)
	}
	return s.res, nil
}

// packScan validates the tree and collects entries, enforcing limits by size.
type packScan struct {
	ctx     context.Context
	lim     Limits
	entries []packEntry
	res     Result
}

func (s *packScan) visit(p string, d fs.DirEntry, err error) error {
	if err != nil {
		return internalErr("walk directory", err)
	}
	if err := ctxErr(s.ctx); err != nil {
		return err
	}
	if p == "." {
		return nil
	}
	if IsIgnored(p) {
		if d.IsDir() {
			return fs.SkipDir
		}
		return nil
	}
	if _, err := cleanName(p); err != nil {
		return err
	}
	return s.add(p, d)
}

func (s *packScan) add(p string, d fs.DirEntry) error {
	typ := d.Type()
	switch {
	case typ&fs.ModeSymlink != 0:
		return pathErr(domain.CodeArchiveLink, "directory contains a symbolic link", p)
	case !d.IsDir() && !typ.IsRegular():
		return pathErr(domain.CodeArchiveSpecialFile, "directory contains a special file", p)
	}
	info, err := d.Info()
	if err != nil {
		return internalErr("stat file", err)
	}
	if d.IsDir() {
		s.entries = append(s.entries, packEntry{rel: p, dir: true, mtime: info.ModTime()})
		return nil
	}
	if err := s.count(p, info.Size()); err != nil {
		return err
	}
	s.entries = append(s.entries, packEntry{rel: p, size: info.Size(), mtime: info.ModTime()})
	return nil
}

func (s *packScan) count(p string, size int64) error {
	if s.res.Files >= s.lim.filesCap() {
		return tooManyErr(s.lim)
	}
	if size > s.lim.fileCap() {
		return fileTooBigErr(s.lim, p)
	}
	if size > s.lim.totalCap()-s.res.Bytes {
		return tooLargeErr(s.lim)
	}
	s.res.Files++
	s.res.Bytes += size
	return nil
}

func writeEntries(ctx context.Context, root *os.Root, entries []packEntry, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if err := writeEntry(ctx, root, tw, e); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return internalErr("finish tar stream", err)
	}
	if err := gz.Close(); err != nil {
		return internalErr("finish gzip stream", err)
	}
	return nil
}

func writeEntry(ctx context.Context, root *os.Root, tw *tar.Writer, e packEntry) error {
	hdr := &tar.Header{Name: e.rel, Mode: int64(filePerm), Size: e.size, ModTime: e.mtime, Typeflag: tar.TypeReg}
	if e.dir {
		hdr = &tar.Header{Name: e.rel + "/", Mode: int64(dirPerm), ModTime: e.mtime, Typeflag: tar.TypeDir}
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return internalErr("write tar header", err)
	}
	if e.dir {
		return nil
	}
	f, err := root.Open(filepath.FromSlash(e.rel))
	if err != nil {
		return internalErr("open file", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.CopyN(tw, ctxReader{ctx: ctx, r: f}, e.size); err != nil {
		if de := (*domain.Error)(nil); errors.As(err, &de) {
			return de
		}
		return internalErr("copy file (changed while packing?)", err)
	}
	return nil
}
