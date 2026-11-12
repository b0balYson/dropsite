package archive

import (
	"context"
	"errors"
	"io"
	"math"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Limits bounds what an archive may contain. A zero or negative value means
// "no limit" for that dimension.
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
