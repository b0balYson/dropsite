package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
)

// maxTrailer bounds the bytes accepted after the tar end-of-archive marker
// (record padding); draining them also verifies the gzip checksum.
const maxTrailer = 1 << 20

// extractTarGz validates and extracts a gzip-compressed tar in one pass.
func extractTarGz(ctx context.Context, r io.Reader, w *writer, t *tree) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return corruptErr(err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil && (hdr == nil || !errors.Is(err, tar.ErrInsecurePath)) {
			return corruptErr(err)
		}
		if err := extractTarEntry(ctx, hdr, tr, w, t); err != nil {
			return err
		}
	}
	return drainTrailer(gz)
}

func extractTarEntry(ctx context.Context, hdr *tar.Header, tr io.Reader, w *writer, t *tree) error {
	if hdr.Typeflag == tar.TypeXGlobalHeader {
		return nil
	}
	typ := tarType(hdr.Typeflag)
	rel, skip, err := t.admit(hdr.Name, typ)
	if err != nil || skip {
		return err
	}
	if typ == typeDir {
		return w.mkdirAll(rel)
	}
	return w.writeFile(ctx, rel, hdr.Name, tr)
}

func drainTrailer(gz io.Reader) error {
	n, err := io.Copy(io.Discard, io.LimitReader(gz, maxTrailer+1))
	if err != nil {
		return corruptErr(err)
	}
	if n > maxTrailer {
		return corruptErr(errors.New("unexpected data after the end of the tar archive"))
	}
	return nil
}

func tarType(flag byte) entryType {
	switch flag {
	case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA still appears in old archives.
		return typeFile
	case tar.TypeDir:
		return typeDir
	case tar.TypeSymlink, tar.TypeLink:
		return typeLink
	}
	return typeSpecial
}
