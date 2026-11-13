package service

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/domain"
)

// Reasons a file of an archive sent to an upload link is skipped.
const (
	SkipSensitive = "looks like a secret (.env, key or credentials file) and is never published this way"
	SkipNotFile   = "not a regular file (link or device)"
)

// capReader fails once more than n bytes were read, so a stream of skipped
// entries or a gzip bomb cannot run past the owner's limits.
type capReader struct {
	r     io.Reader
	n     int64
	limit int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		return n, domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "the archive exceeds the site size limit").
			WithParams(map[string]any{"limit": c.limit})
	}
	return n, err
}

// tarStream opens a tar or gzip-compressed tar stream (detected by its
// magic bytes) with every layer bounded by limit.
func tarStream(body io.Reader, limit int64) (*tar.Reader, func(), error) {
	br := bufio.NewReader(&capReader{r: body, limit: limit})
	magic, err := br.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, bodyError(err)
	}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, errCorruptTar()
		}
		return tar.NewReader(&capReader{r: gz, limit: limit}), func() { _ = gz.Close() }, nil
	}
	return tar.NewReader(br), func() {}, nil
}

func errCorruptTar() error {
	return domain.E(domain.KindInvalid, domain.CodeArchiveCorrupt,
		"the body is not a valid tar stream; send the output of tar -cf - (optionally gzip-compressed)")
}

// tarEntryPath returns the site path of a tar entry name ("./css/a.css"
// becomes "css/a.css"), or "" for the root entry.
func tarEntryPath(name string) string {
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	name = strings.TrimSuffix(name, "/")
	if name == "." {
		return ""
	}
	return name
}

// macOS tar adds AppleDouble companions ("._name") for extended attributes.
func appleDouble(p string) bool { return strings.HasPrefix(path.Base(p), "._") }

// PutLinkArchive stores the files of a tar stream (a folder) sent to an
// upload link. Service files (.git/, .DS_Store, node_modules/) are skipped
// silently, secret-looking files and links are skipped and reported, as
// dropsite deploy --skip-sensitive does. Either every file is taken or none.
func (s *Service) PutLinkArchive(ctx context.Context, token, ip string, body io.Reader) (LinkReceipt, error) {
	u, err := s.linkSession(ctx, token, ip)
	if err != nil {
		return LinkReceipt{}, err
	}
	tr, done, err := tarStream(body, uploadLimit(u.lim))
	if err != nil {
		return LinkReceipt{}, err
	}
	defer done()
	st, err := s.newStage()
	if err != nil {
		return LinkReceipt{}, err
	}
	defer st.close(s)
	skipped, err := receiveTar(ctx, tr, st, u)
	if err != nil {
		return LinkReceipt{}, err
	}
	if len(st.files) == 0 {
		return LinkReceipt{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			"the archive has no files to upload; pack the site folder with tar -C <folder> -cf - .").
			WithParams(map[string]any{"skipped": len(skipped)})
	}
	rec, err := s.place(u, st)
	if err != nil {
		return LinkReceipt{}, err
	}
	rec.Skipped = skipped
	return rec, nil
}

// receiveTar stages the regular files of tr and returns the skipped ones.
func receiveTar(ctx context.Context, tr *tar.Reader, st *stage, u *uploadSession) ([]SkippedFile, error) {
	maxEntries := 4*u.lim.MaxFiles + 1000
	var skipped []SkippedFile
	var total int64
	for entries := 0; ; entries++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entries > maxEntries {
			return nil, domain.E(domain.KindTooLarge, domain.CodeArchiveTooMany, "the archive has too many entries").
				WithParams(map[string]any{"limit": u.lim.MaxFiles})
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return skipped, nil
		}
		if err != nil {
			var de *domain.Error
			var mb *http.MaxBytesError
			if errors.As(err, &de) || errors.As(err, &mb) {
				return nil, bodyError(err)
			}
			return nil, errCorruptTar()
		}
		p := tarEntryPath(hdr.Name)
		if p == "" || hdr.Typeflag == tar.TypeDir || archive.IsIgnored(p) || appleDouble(p) {
			continue
		}
		if archive.IsSensitive(p) {
			skipped = append(skipped, SkippedFile{Path: p, Reason: SkipSensitive})
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			skipped = append(skipped, SkippedFile{Path: p, Reason: SkipNotFile})
			continue
		}
		clean, err := checkWritePath(p)
		if err != nil {
			return nil, err
		}
		if total += hdr.Size; u.lim.MaxSiteBytes > 0 && total > u.lim.MaxSiteBytes {
			return nil, domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "the uploaded files exceed the site size limit").
				WithParams(map[string]any{"limit": u.lim.MaxSiteBytes})
		}
		if err := st.receive(clean, tr, u.lim.MaxFileBytes); err != nil {
			return nil, err
		}
		if u.lim.MaxFiles > 0 && len(st.files) > u.lim.MaxFiles {
			return nil, domain.E(domain.KindTooLarge, domain.CodeArchiveTooMany, "the upload has too many files").
				WithParams(map[string]any{"limit": u.lim.MaxFiles})
		}
	}
}
