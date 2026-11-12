package archive

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

// MaxDepth is the largest number of path segments a stored entry may have
// (a file at depth MaxDepth sits in MaxDepth-1 nested directories).
const MaxDepth = 32

// MaxPathLen is the longest normalized relative path, in bytes, of a stored entry.
const MaxPathLen = 1024

// Limits bounds what an archive may contain. A zero or negative value means
// "no limit" for that dimension.
//
// Directories (explicit entries and implied parents) are capped separately at
// MaxFiles as well: every directory costs an inode and a mkdir under the site
// lock just like a file, yet carries no bytes, so without a cap a tiny archive
// of empty directories exhausts inodes. A real site rarely has more
// directories than files, so the same number keeps the rule simple to explain
// while bounding the total number of entries at 2*MaxFiles.
type Limits struct {
	MaxTotalBytes int64
	MaxFileBytes  int64
	MaxFiles      int
}

// Result describes the stored content: files written and their total size.
type Result struct {
	Files int
	Bytes int64
}

func (l Limits) fileCap() int64 { return capOf(l.MaxFileBytes) }

func (l Limits) totalCap() int64 { return capOf(l.MaxTotalBytes) }

func (l Limits) filesCap() int {
	if l.MaxFiles <= 0 {
		return math.MaxInt
	}
	return l.MaxFiles
}

// dirsCap is the directory cap; it follows MaxFiles (see Limits).
func (l Limits) dirsCap() int { return l.filesCap() }

func capOf(v int64) int64 {
	if v <= 0 {
		return math.MaxInt64
	}
	return v
}

func tooManyErr(lim Limits) *domain.Error {
	return fail(domain.CodeArchiveTooMany, "archive has too many files",
		map[string]any{"limit": lim.MaxFiles})
}

func tooManyDirsErr(lim Limits) *domain.Error {
	return fail(domain.CodeArchiveTooMany, "archive has too many directories",
		map[string]any{"limit": lim.MaxFiles, "entries": "directories"})
}

// checkShape bounds the nesting depth and the length of a stored path. It is
// applied only to entries that would be stored: skipped entries cost nothing.
func checkShape(rel, raw string) error {
	if len(rel) <= MaxPathLen && strings.Count(rel, "/")+1 <= MaxDepth {
		return nil
	}
	return fail(domain.CodeArchiveBadName, "entry path is nested too deeply or is too long",
		map[string]any{"path": raw, "max_depth": MaxDepth, "max_path_len": MaxPathLen})
}

func fileTooBigErr(lim Limits, path string) *domain.Error {
	return fail(domain.CodeArchiveFileTooBig, "file exceeds the size limit",
		map[string]any{"limit": lim.MaxFileBytes, "path": path})
}

func tooLargeErr(lim Limits) *domain.Error {
	return fail(domain.CodeArchiveTooLarge, "unpacked size exceeds the limit",
		map[string]any{"limit": lim.MaxTotalBytes})
}

// budget counts bytes actually read from the archive against the limits.
type budget struct {
	lim   Limits
	bytes int64
	buf   []byte
}

const copyBufSize = 256 << 10

func newBudget(lim Limits) *budget {
	return &budget{lim: lim, buf: make([]byte, copyBufSize)}
}

// copy streams src into dst and aborts as soon as the bytes read exceed the
// per-file or the remaining total limit, regardless of what headers claim.
// Read failures are reported as a corrupt archive, write failures as internal.
func (b *budget) copy(ctx context.Context, dst io.Writer, src io.Reader, path string) error {
	fileCap := b.lim.fileCap()
	left := b.lim.totalCap() - b.bytes
	var n int64
	for {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		m, rerr := src.Read(b.buf)
		if m > 0 {
			n += int64(m)
			if n > fileCap {
				return fileTooBigErr(b.lim, path)
			}
			if n > left {
				return tooLargeErr(b.lim)
			}
			if _, werr := dst.Write(b.buf[:m]); werr != nil {
				return internalErr("write extracted file", werr)
			}
		}
		if errors.Is(rerr, io.EOF) {
			b.bytes += n
			return nil
		}
		if rerr != nil {
			return corruptErr(rerr)
		}
	}
}

// ctxReader fails reads once ctx is done, so long copies stop promptly.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := ctxErr(c.ctx); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
