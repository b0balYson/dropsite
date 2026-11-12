package archive

import (
	"bytes"
	"errors"
	"io"

	"github.com/b0balYson/dropsite/internal/domain"
)

type format int

const (
	formatZip format = iota + 1
	formatTarGz
)

var (
	magicZip      = []byte("PK\x03\x04")
	magicZipEmpty = []byte("PK\x05\x06")
	magicGzip     = []byte{0x1f, 0x8b}
)

// detect identifies the archive format by its leading bytes, never by name.
func detect(r io.ReaderAt) (format, error) {
	head := make([]byte, 4)
	n, err := r.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, internalErr("read archive header", err)
	}
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, magicZip), bytes.HasPrefix(head, magicZipEmpty):
		return formatZip, nil
	case bytes.HasPrefix(head, magicGzip):
		return formatTarGz, nil
	}
	return 0, fail(domain.CodeArchiveFormat, "unsupported archive format: use zip or tar.gz", nil)
}
