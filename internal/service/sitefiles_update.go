package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

// MaxDeletePaths caps the delete list of one call.
const MaxDeletePaths = 100_000

// FileChanges are the edits of update_site_files.
type FileChanges struct {
	Put    []InlineFile
	Delete []string
	Fetch  []FetchSpec
}

// UpdateResult is the outcome of an edit or an upload commit.
type UpdateResult struct {
	DeployResult
	Base    string
	Written int
	Deleted int
	Fetched []FetchedFile
}

// overlayFile is a file written on top of the base release: bytes in memory
// or a file in an upload session.
type overlayFile struct {
	data []byte
	root *os.Root
	name string
}

func (o overlayFile) open() (io.ReadCloser, int64, error) {
	if o.root == nil {
		return io.NopCloser(bytes.NewReader(o.data)), int64(len(o.data)), nil
	}
	f, err := o.root.Open(o.name)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func errConflict(current string) error {
	msg := "the site has changed since base_deploy_id was read: its current release is " + current +
		"; call list_site_files and read_site_files again, redo the change on the current files, " +
		"and pass base_deploy_id " + current
	if current == "" {
		msg = "the site has no published release now; publish it whole with deploy_files or begin_upload in replace mode"
	}
	return domain.E(domain.KindConflict, domain.CodeDeployConflict, msg).
		WithParams(map[string]any{"current": current})
}

// checkBase reloads the site and fails with deploy_conflict unless its
// current release is base.
func (s *Service) checkBase(ctx context.Context, siteID, base string) (domain.Site, error) {
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		return domain.Site{}, err
	}
	cur := ""
	if site.CurrentDeployID != nil {
		cur = *site.CurrentDeployID
	}
	if cur != base {
		return domain.Site{}, errConflict(cur)
	}
	return site, nil
}

func requireBase(base string) error {
	if strings.TrimSpace(base) == "" {
		return domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			"base_deploy_id is required: call list_site_files first and pass the deploy_id it returned")
	}
	return nil
}

// decodePuts validates and decodes inline files; taken collects their paths.
func decodePuts(files []InlineFile, taken map[string]bool) (map[string]overlayFile, error) {
	if len(files) > MaxInlineFiles {
		return nil, errFilesTooLarge()
	}
	out := make(map[string]overlayFile, len(files))
	total := 0
	for _, f := range files {
		p, err := checkWritePath(f.Path)
		if err != nil {
			return nil, err
		}
		if taken[p] {
			return nil, invalidFile(p, "the path is listed twice")
		}
		taken[p] = true
		data, err := decodeInline(f)
		if err != nil {
			return nil, err
		}
		if total += len(data); total > MaxInlineBytes {
			return nil, errFilesTooLarge()
		}
		out[p] = overlayFile{data: data}
	}
	return out, nil
}

// checkDeletes validates delete paths: each names a file or a directory.
func checkDeletes(paths []string, taken map[string]bool) ([]string, error) {
	if len(paths) > MaxDeletePaths {
		return nil, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			fmt.Sprintf("at most %d paths to delete per call", MaxDeletePaths))
	}
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		p, err := cleanInlinePath(strings.TrimSuffix(raw, "/"))
		if err != nil {
			return nil, err
		}
		if taken[p] {
			return nil, invalidFile(p, "the same path is both written and deleted")
		}
		out = append(out, p)
	}
	return out, nil
}

// UpdateSiteFiles creates a new release from base_deploy_id plus the
// changes, through the regular pipeline. It fails with deploy_conflict when
// the site's current release is no longer base.
func (s *Service) UpdateSiteFiles(ctx context.Context, a Actor, ref, base string, ch FileChanges) (UpdateResult, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return UpdateResult{}, err
	}
	if err := requireBase(base); err != nil {
		return UpdateResult{}, err
	}
	taken := map[string]bool{}
	overlay, err := decodePuts(ch.Put, taken)
	if err != nil {
		return UpdateResult{}, err
	}
	reqs, err := checkFetchSpecs(ch.Fetch, taken)
	if err != nil {
		return UpdateResult{}, err
	}
	dels, err := checkDeletes(ch.Delete, taken)
	if err != nil {
		return UpdateResult{}, err
	}
	if len(overlay)+len(reqs)+len(dels) == 0 {
		return UpdateResult{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			"nothing to change: pass files, delete or fetch")
	}
	if _, err := s.checkBase(ctx, site.ID, base); err != nil {
		return UpdateResult{}, err
	}
	lim, err := s.siteOwnerLimits(ctx, site)
	if err != nil {
		return UpdateResult{}, err
	}
	if ok, retry := s.deployLimit.Allow(userKey(a.User.ID)); !ok {
		return UpdateResult{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": DeploysPerHour})
	}
	got, err := s.fetchAssets(ctx, a, reqs, lim)
	if err != nil {
		return UpdateResult{}, err
	}
	for _, f := range got {
		overlay[f.Path] = overlayFile{data: f.Data}
	}
	res, err := s.publishEdit(ctx, a, site.ID, base, dels, overlay, lim, "mcp_update")
	if err != nil {
		return UpdateResult{}, err
	}
	res.Fetched = fetchedReport(got)
	res.Written = len(overlay)
	return res, nil
}

// publishEdit takes the site lock, re-checks the base (empty base: a full
// replacement), packs base − dels + overlay and publishes it.
func (s *Service) publishEdit(ctx context.Context, a Actor, siteID, base string, dels []string,
	overlay map[string]overlayFile, lim domain.Limits, reason string) (UpdateResult, error) {
	return s.publishEditAs(ctx, a, siteID, base, dels, overlay, lim, reason, false)
}

// publishEditAs is publishEdit; with prepare the release is created without
// switching the site to it (a scheduled publish switches later).
func (s *Service) publishEditAs(ctx context.Context, a Actor, siteID, base string, dels []string,
	overlay map[string]overlayFile, lim domain.Limits, reason string, prepare bool) (UpdateResult, error) {
	unlock := s.siteLocks.lock(siteID)
	defer unlock()
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		return UpdateResult{}, err
	}
	var baseRoot *os.Root
	if base != "" {
		if site, err = s.checkBase(ctx, siteID, base); err != nil {
			return UpdateResult{}, err
		}
		d, err := s.repo.GetDeploy(ctx, siteID, base)
		if err != nil {
			return UpdateResult{}, err
		}
		if baseRoot, err = s.openRelease(site, d); err != nil {
			return UpdateResult{}, err
		}
		defer func() { _ = baseRoot.Close() }()
	}
	upload, removed, err := s.packEdit(baseRoot, dels, overlay)
	if err != nil {
		return UpdateResult{}, err
	}
	defer s.removeUpload(upload)
	var d domain.Deploy
	if prepare {
		d, err = s.prepareRelease(ctx, a, site, upload, lim, domain.SourceMCP)
	} else {
		d, err = s.publish(ctx, a, site, upload, lim, domain.SourceMCP)
	}
	if err != nil {
		return UpdateResult{}, err
	}
	if !prepare {
		s.prune(ctx, site.ID, lim.KeepReleases)
	}
	meta := map[string]any{
		"deploy": d.ID, "source": domain.SourceMCP, "files": d.FileCount, "bytes": d.SizeBytes,
		"from": base, "reason": reason, "written": len(overlay), "deleted": removed,
	}
	if prepare {
		meta["prepared"] = true
	}
	s.audit(ctx, a, "deploy.create", siteTarget(site.ID), meta)
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return UpdateResult{}, err
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{DeployResult: DeployResult{Deploy: d, Site: v, Quality: s.reportOf(ctx, site.ID, d.ID)},
		Base: base, Deleted: removed}, nil
}

func (s *Service) removeUpload(name string) {
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("remove upload failed", "path", name, "err", err)
	}
}

// packEdit writes a tar.gz of base − dels + overlay into the upload
// directory and returns its name and the number of base files removed.
// Every delete path must match a base file or directory.
func (s *Service) packEdit(base *os.Root, dels []string, overlay map[string]overlayFile) (string, int, error) {
	f, err := os.CreateTemp(s.uploadDir, "upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create upload file: %w", err)
	}
	name := f.Name()
	removed, packErr := packTo(f, base, dels, overlay)
	if err := errors.Join(packErr, f.Close()); err != nil {
		_ = os.Remove(name)
		var de *domain.Error
		if errors.As(err, &de) {
			return "", 0, de
		}
		return "", 0, fmt.Errorf("pack release: %w", err)
	}
	return name, removed, nil
}

func packTo(w io.Writer, base *os.Root, dels []string, overlay map[string]overlayFile) (int, error) {
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return 0, err
	}
	tw := tar.NewWriter(gz)
	removed := 0
	used := make([]bool, len(dels))
	if base != nil {
		err := walkFiles(base, func(p string, _ fs.FileInfo) error {
			gone := false
			for i, d := range dels {
				if p == d || strings.HasPrefix(p, d+"/") {
					used[i], gone = true, true
				}
			}
			if gone {
				removed++
				return nil
			}
			if _, ok := overlay[p]; ok {
				return nil
			}
			return addEntry(tw, p, overlayFile{root: base, name: p})
		})
		if err != nil {
			return 0, err
		}
	}
	for i, d := range dels {
		if !used[i] {
			return 0, invalidFile(d, "no such file or directory in the base release; call list_site_files")
		}
	}
	names := make([]string, 0, len(overlay))
	for p := range overlay {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		if err := addEntry(tw, p, overlay[p]); err != nil {
			return 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	return removed, gz.Close()
}

func addEntry(tw *tar.Writer, p string, o overlayFile) error {
	r, size, err := o.open()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, r, size)
	return err
}
