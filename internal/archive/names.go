package archive

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
)

// maxSegmentLen is the longest path component common file systems accept.
const maxSegmentLen = 255

// ignoredSegments are service names skipped at any depth.
var ignoredSegments = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__MACOSX":     true,
	".DS_Store":    true,
	"Thumbs.db":    true,
}

// IsIgnored reports whether a slash-separated relative path is silently skipped
// (.git/, .DS_Store, __MACOSX/, Thumbs.db, node_modules/ at any depth, and
// brand.ProjectFile at the root).
func IsIgnored(rel string) bool {
	rel = strings.Trim(rel, "/")
	if rel == brand.ProjectFile {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		if ignoredSegments[seg] {
			return true
		}
	}
	return false
}

// cleanName validates a raw entry name before any normalization and returns
// the normalized slash-separated relative path. "." segments are dropped; an
// empty result denotes the archive root itself.
func cleanName(raw string) (string, error) {
	if raw == "" {
		return "", pathErr(domain.CodeArchiveBadName, "entry has an empty name", raw)
	}
	if isAbsolute(raw) {
		return "", pathErr(domain.CodeArchiveAbsPath, "entry has an absolute path", raw)
	}
	if hasBadRunes(raw) {
		return "", pathErr(domain.CodeArchiveBadName, "entry name contains control characters, backslashes or invalid UTF-8", raw)
	}
	segs, err := splitSegments(raw)
	if err != nil {
		return "", err
	}
	rel := strings.Join(segs, "/")
	if rel != "" && !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", pathErr(domain.CodeArchiveBadName, "entry name is not a local path", raw)
	}
	return rel, nil
}

func isAbsolute(raw string) bool {
	if raw[0] == '/' || raw[0] == '\\' {
		return true
	}
	return len(raw) >= 2 && raw[1] == ':' && isASCIILetter(raw[0])
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func hasBadRunes(raw string) bool {
	if !utf8.ValidString(raw) {
		return true
	}
	for _, r := range raw {
		if r == '\\' || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func splitSegments(raw string) ([]string, error) {
	parts := strings.Split(strings.TrimSuffix(raw, "/"), "/")
	segs := make([]string, 0, len(parts))
	for _, seg := range parts {
		switch {
		case seg == "..":
			return nil, pathErr(domain.CodeArchiveTraversal, "entry path contains '..'", raw)
		case seg == ".":
			continue
		case seg == "":
			return nil, pathErr(domain.CodeArchiveBadName, "entry path has an empty segment", raw)
		case len(seg) > maxSegmentLen:
			return nil, pathErr(domain.CodeArchiveBadName, "entry path segment is too long", raw)
		}
		segs = append(segs, seg)
	}
	return segs, nil
}
