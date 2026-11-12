package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Inline publication limits (spec mcp-server).
const (
	MaxInlineBytes = 16 << 20
	MaxInlineFiles = 20_000
	maxInlinePath  = 512
)

// InlineFile is one file passed by value, as MCP tools do.
type InlineFile struct {
	Path     string
	Content  string
	Encoding string // "utf8" (default) or "base64"
}

func errFilesTooLarge() error {
	return domain.E(domain.KindTooLarge, domain.CodeFilesTooLarge,
		"the files are larger than 1 MB in total; send them in parts with begin_upload, upload_files and "+
			"commit_upload, or publish this site with the dropsite CLI "+
			"(dropsite deploy <folder>), from Claude Desktop, or upload a zip in the web interface").
		WithParams(map[string]any{"limit": MaxInlineBytes})
}

func invalidFile(p, msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeInvalidInput, fmt.Sprintf("file %q: %s", p, msg)).
		WithParams(map[string]any{"file": p})
}

// cleanInlinePath validates a relative slash-separated path.
func cleanInlinePath(p string) (string, error) {
	if p == "" || len(p) > maxInlinePath || !utf8.ValidString(p) {
		return "", invalidFile(p, "the path must be a non-empty relative path such as index.html or css/site.css")
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) {
		return "", invalidFile(p, "use a relative path with forward slashes, e.g. assets/app.js")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", invalidFile(p, "the path must not contain empty, '.' or '..' segments")
		}
	}
	return path.Clean(p), nil
}

func decodeInline(f InlineFile) ([]byte, error) {
	switch f.Encoding {
	case "", "utf8", "utf-8":
		return []byte(f.Content), nil
	case "base64":
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(f.Content))
		if err != nil {
			return nil, invalidFile(f.Path, "content is not valid base64 (standard alphabet with padding)")
		}
		return b, nil
	}
	return nil, invalidFile(f.Path, `encoding must be "utf8" or "base64"`)
}

// buildInlineArchive validates the files and packs them into a tar.gz.
func buildInlineArchive(files []InlineFile) ([]byte, error) {
	if len(files) == 0 {
		return nil, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "pass at least one file, including index.html")
	}
	if len(files) > MaxInlineFiles {
		return nil, errFilesTooLarge()
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	seen := map[string]bool{}
	total := 0
	for _, f := range files {
		p, err := cleanInlinePath(f.Path)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, invalidFile(p, "the path is listed twice")
		}
		seen[p] = true
		data, err := decodeInline(f)
		if err != nil {
			return nil, err
		}
		if total += len(data); total > MaxInlineBytes {
			return nil, errFilesTooLarge()
		}
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, fmt.Errorf("pack %s: %w", p, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("pack %s: %w", p, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("pack files: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("pack files: %w", err)
	}
	return buf.Bytes(), nil
}

// DeployFiles publishes files passed by value as a new release with source
// mcp. Without ref a new site titled title is created first (only after the
// files passed validation). The archive goes through the regular pipeline.
func (s *Service) DeployFiles(ctx context.Context, a Actor, ref, title string, files []InlineFile) (DeployResult, error) {
	archive, err := buildInlineArchive(files)
	if err != nil {
		return DeployResult{}, err
	}
	if ref == "" {
		if strings.TrimSpace(title) == "" {
			title = "Untitled site"
		}
		v, err := s.CreateSite(ctx, a, title, "")
		if err != nil {
			return DeployResult{}, err
		}
		ref = v.Site.ID
	}
	return s.Deploy(ctx, a, ref, bytes.NewReader(archive), domain.SourceMCP)
}
