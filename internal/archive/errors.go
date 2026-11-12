// Package archive validates and extracts uploaded site archives (zip and
// tar.gz) and packs local directories for the CLI. It knows nothing about
// sites or the database: input is a file, a destination and limits; output is
// a size, a file count or a *domain.Error with a stable code.
package archive

import (
	"context"

	"github.com/b0balYson/dropsite/internal/domain"
)

// fail builds an archive validation error. Size limits map to KindTooLarge,
// everything else to KindInvalid.
func fail(code, message string, params map[string]any) *domain.Error {
	kind := domain.KindInvalid
	switch code {
	case domain.CodeArchiveTooLarge, domain.CodeArchiveFileTooBig, domain.CodeArchiveTooMany:
		kind = domain.KindTooLarge
	}
	e := domain.E(kind, code, message)
	if params != nil {
		e = e.WithParams(params)
	}
	return e
}

// pathErr builds a validation error naming the offending archive entry.
func pathErr(code, message, path string) *domain.Error {
	return fail(code, message, map[string]any{"path": path})
}

func corruptErr(cause error) *domain.Error {
	return fail(domain.CodeArchiveCorrupt, "archive is corrupt or truncated", nil).WithCause(cause)
}

func internalErr(message string, cause error) *domain.Error {
	return domain.E(domain.KindInternal, domain.CodeInternal, message).WithCause(cause)
}

// ctxErr reports cancellation as an internal error that still unwraps to the
// context error, so callers can use errors.Is(err, context.Canceled).
func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return internalErr("archive processing cancelled", err)
	}
	return nil
}
