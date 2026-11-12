package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/optimize"
	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/tracking"
)

// Reading site files through MCP (spec site-files).
const (
	MaxListEntries     = 100_000
	MaxReadPaths       = 2000
	MaxReadBytes       = 8 << 20
	MaxReadBinaryBytes = 4 << 20
)

// SiteFile describes one regular file of a release.
type SiteFile struct {
	Path   string
	Size   int64
	SHA256 string
	Type   string
}

// SiteFileList is the listing of a release.
type SiteFileList struct {
	Site       SiteView
	Deploy     domain.Deploy
	Current    bool
	Files      []SiteFile
	TotalFiles int
	TotalBytes int64
	Truncated  bool
}

// SiteFileContent is a read file. Content is nil when it was not included;
// Note then says why and what to do.
type SiteFileContent struct {
	SiteFile
	Text    bool
	Content []byte
	Note    string
}

// SiteFileRead is the result of ReadSiteFiles.
type SiteFileRead struct {
	Site      SiteView
	Deploy    domain.Deploy
	Current   bool
	Files     []SiteFileContent
	Missing   []string
	Truncated bool
}

// releaseFor resolves a site the actor may see and one of its ready
// releases (the current one when deployID is empty).
func (s *Service) releaseFor(ctx context.Context, a Actor, ref, deployID string) (domain.Site, domain.Deploy, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return domain.Site{}, domain.Deploy{}, err
	}
	if deployID == "" {
		if site.CurrentDeployID == nil {
			return domain.Site{}, domain.Deploy{}, domain.E(domain.KindInvalid, domain.CodeSiteNotPublished,
				"the site has no published release yet; publish it with deploy_files or begin_upload first")
		}
		deployID = *site.CurrentDeployID
	}
	if !storage.ValidDeployID(deployID) {
		return domain.Site{}, domain.Deploy{}, domain.ErrNotFound
	}
	d, err := s.repo.GetDeploy(ctx, site.ID, deployID)
	if err != nil {
		return domain.Site{}, domain.Deploy{}, err
	}
	if d.Status != domain.DeployReady {
		return domain.Site{}, domain.Deploy{}, domain.E(domain.KindInvalid, domain.CodeDeployNotReady,
			"this release is no longer available; call list_deploys")
	}
	return site, d, nil
}

func (s *Service) openRelease(site domain.Site, d domain.Deploy) (*os.Root, error) {
	root, err := s.files.OpenRelease(site.ID, d.ID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, domain.E(domain.KindInvalid, domain.CodeDeployNotReady, "this release is no longer available")
	}
	if err != nil {
		return nil, fmt.Errorf("open release: %w", err)
	}
	return root, nil
}

// walkFiles calls fn for every regular file of root in lexical order.
// Symbolic links and special files are skipped: releases never contain them,
// and nothing outside the release may be reached through one. Files the
// platform generated at publish time (optimize.Dir) are not the site's own
// and are skipped too.
func walkFiles(root *os.Root, fn func(p string, info fs.FileInfo) error) error {
	return fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if optimize.IsGenerated(p) {
			return fs.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(p, info)
	})
}

func fileType(p string) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(p))); t != "" {
		if mt, _, err := mime.ParseMediaType(t); err == nil {
			return mt
		}
	}
	return "application/octet-stream"
}

func hashFile(root *os.Root, p string) (string, error) {
	f, err := root.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ListSiteFiles lists the regular files of a release (the current one by
// default), at most MaxListEntries, optionally under prefix.
func (s *Service) ListSiteFiles(ctx context.Context, a Actor, ref, deployID, prefix string) (SiteFileList, error) {
	site, d, err := s.releaseFor(ctx, a, ref, deployID)
	if err != nil {
		return SiteFileList{}, err
	}
	root, err := s.openRelease(site, d)
	if err != nil {
		return SiteFileList{}, err
	}
	defer func() { _ = root.Close() }()
	prefix = strings.TrimPrefix(prefix, "/")
	out := SiteFileList{Deploy: d, Current: site.CurrentDeployID != nil && *site.CurrentDeployID == d.ID}
	err = walkFiles(root, func(p string, info fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		out.TotalFiles++
		out.TotalBytes += info.Size()
		if !strings.HasPrefix(p, prefix) {
			return nil
		}
		if len(out.Files) >= MaxListEntries {
			out.Truncated = true
			return nil
		}
		sum, err := hashFile(root, p)
		if err != nil {
			return err
		}
		out.Files = append(out.Files, SiteFile{Path: p, Size: info.Size(), SHA256: sum, Type: fileType(p)})
		return nil
	})
	if err != nil {
		return SiteFileList{}, fmt.Errorf("list release: %w", err)
	}
	out.Site, err = s.view(ctx, site)
	return out, err
}

func hasGlob(p string) bool { return strings.ContainsAny(p, "*?[") }

// readTargets expands the requested paths and patterns to release files, in
// request order without duplicates; unknown exact paths go to missing.
func readTargets(root *os.Root, patterns []string) (targets, missing []string, err error) {
	if len(patterns) == 0 || len(patterns) > MaxReadPaths {
		return nil, nil, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			fmt.Sprintf("pass between 1 and %d paths or patterns", MaxReadPaths))
	}
	var all []string
	seen := map[string]bool{}
	for _, raw := range patterns {
		p, err := cleanInlinePath(strings.TrimPrefix(raw, "/"))
		if err != nil {
			return nil, nil, err
		}
		if !hasGlob(p) {
			if !seen[p] {
				seen[p] = true
				targets = append(targets, p)
			}
			continue
		}
		if _, err := path.Match(p, ""); err != nil {
			return nil, nil, invalidFile(raw, "the pattern is malformed")
		}
		if all == nil {
			if err := walkFiles(root, func(f string, _ fs.FileInfo) error { all = append(all, f); return nil }); err != nil {
				return nil, nil, fmt.Errorf("list release: %w", err)
			}
		}
		matched := false
		for _, f := range all {
			if ok, _ := path.Match(p, f); ok {
				matched = true
				if !seen[f] {
					seen[f] = true
					targets = append(targets, f)
				}
			}
		}
		if !matched {
			missing = append(missing, raw)
		}
	}
	return targets, missing, nil
}

// ReadSiteFiles returns the content of release files (the current release by
// default). Text comes back as is (the platform's analytics block removed
// from HTML), small binaries as bytes, everything else as metadata with a
// note; at most MaxReadBytes of content per call.
func (s *Service) ReadSiteFiles(ctx context.Context, a Actor, ref, deployID string, paths []string) (SiteFileRead, error) {
	site, d, err := s.releaseFor(ctx, a, ref, deployID)
	if err != nil {
		return SiteFileRead{}, err
	}
	root, err := s.openRelease(site, d)
	if err != nil {
		return SiteFileRead{}, err
	}
	defer func() { _ = root.Close() }()
	targets, missing, err := readTargets(root, paths)
	if err != nil {
		return SiteFileRead{}, err
	}
	out := SiteFileRead{Deploy: d, Current: site.CurrentDeployID != nil && *site.CurrentDeployID == d.ID, Missing: missing}
	budget := MaxReadBytes
	for _, p := range targets {
		if err := ctx.Err(); err != nil {
			return SiteFileRead{}, err
		}
		fc, ok, err := readOne(root, p, &budget)
		if err != nil {
			return SiteFileRead{}, fmt.Errorf("read %s: %w", p, err)
		}
		if !ok {
			out.Missing = append(out.Missing, p)
			continue
		}
		if fc.Content == nil && strings.Contains(fc.Note, "separate call") {
			out.Truncated = true
		}
		out.Files = append(out.Files, fc)
	}
	out.Site, err = s.view(ctx, site)
	return out, err
}

// readOne reads one file; ok is false when p is not a regular file of the
// release (missing, a directory or a link).
func readOne(root *os.Root, p string, budget *int) (SiteFileContent, bool, error) {
	if optimize.IsGenerated(p) {
		return SiteFileContent{}, false, nil
	}
	info, err := root.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return SiteFileContent{}, false, nil
	}
	if err != nil {
		// Paths that leave the root are reported as missing, never read.
		return SiteFileContent{}, false, nil //nolint:nilerr // an escaping path is "not in this release"
	}
	sum, err := hashFile(root, p)
	if err != nil {
		return SiteFileContent{}, false, err
	}
	fc := SiteFileContent{SiteFile: SiteFile{Path: p, Size: info.Size(), SHA256: sum, Type: fileType(p)}}
	if info.Size() > MaxReadBytes {
		fc.Note = fmt.Sprintf("not included: the file is larger than %d KB; it can only be replaced as a whole "+
			"(update_site_files with new content or a fetch URL)", MaxReadBytes>>10)
		return fc, true, nil
	}
	data, err := root.ReadFile(p)
	if err != nil {
		return SiteFileContent{}, false, err
	}
	fc.Text = utf8.Valid(data) && !bytes.Contains(data, []byte{0})
	if fc.Text && tracking.IsHTML(p) {
		data = tracking.Apply(data, nil)
	}
	switch {
	case !fc.Text && len(data) > MaxReadBinaryBytes:
		fc.Note = fmt.Sprintf("binary file larger than %d KB, content not included; to change it, upload a new "+
			"file at this path with update_site_files (content or fetch URL)", MaxReadBinaryBytes>>10)
	case len(data) > *budget:
		fc.Note = fmt.Sprintf("not included: this response already holds %d KB; read this file in a separate call",
			(MaxReadBytes-*budget)>>10)
	default:
		*budget -= len(data)
		fc.Content = data
	}
	return fc, true, nil
}
