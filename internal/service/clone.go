package service

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/tracking"
)

// copySuffix marks the default title of a site copy.
const copySuffix = " (copy)"

// CloneSite creates a new site of the actor from the current release of a
// visible site. Only the files are copied: forms, collections, domains, the
// slug, access settings and counters stay with the source, and the counter
// block Dropsite inserted into the source's pages is removed.
func (s *Service) CloneSite(ctx context.Context, a Actor, ref, title, slug string, source domain.DeploySource) (DeployResult, error) {
	if !source.Valid() {
		source = domain.SourceAPI
	}
	src, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return DeployResult{}, err
	}
	if src.CurrentDeployID == nil {
		return DeployResult{}, domain.E(domain.KindInvalid, domain.CodeSiteNotPublished,
			"the site has no published version to copy")
	}
	if strings.TrimSpace(title) == "" {
		title = copyTitle(src.Title)
	}
	if _, err := domain.ValidateTitle(title); err != nil {
		return DeployResult{}, err
	}
	d, err := s.repo.GetDeploy(ctx, src.ID, *src.CurrentDeployID)
	if err != nil {
		return DeployResult{}, err
	}
	upload, err := s.packCopy(src, d)
	if err != nil {
		return DeployResult{}, err
	}
	defer s.removeUpload(upload)
	if ok, retry := s.deployLimit.Allow(userKey(a.User.ID)); !ok {
		return DeployResult{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": DeploysPerHour})
	}
	res, err := s.publishNewSite(ctx, a, title, slug, upload, source, map[string]any{
		"reason": "clone", "from_site": src.ID, "from_deploy": d.ID,
	})
	if err != nil {
		return DeployResult{}, err
	}
	s.audit(ctx, a, "site.clone", siteTarget(res.Site.Site.ID), map[string]any{"from_site": src.ID, "from_deploy": d.ID})
	return res, nil
}

// copyTitle appends the copy suffix, shortening the title to fit.
func copyTitle(t string) string {
	room := domain.MaxTitleLength - utf8.RuneCountInString(copySuffix)
	if r := []rune(t); len(r) > room {
		t = strings.TrimSpace(string(r[:room]))
	}
	return t + copySuffix
}

// packCopy packs the files of a release into an upload with Dropsite's
// counter block removed from every page.
func (s *Service) packCopy(site domain.Site, d domain.Deploy) (string, error) {
	root, err := s.openRelease(site, d)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	overlay := map[string]overlayFile{}
	err = walkFiles(root, func(p string, _ fs.FileInfo) error {
		if !tracking.IsHTML(p) {
			return nil
		}
		content, err := root.ReadFile(p)
		if err != nil {
			return err
		}
		if clean := tracking.Apply(content, nil); len(clean) != len(content) {
			overlay[p] = overlayFile{data: clean}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	upload, _, err := s.packEdit(root, nil, overlay)
	return upload, err
}

// publishNewSite creates a site and publishes the packed upload as its first
// release. When the publication fails, the new empty site is deleted again.
func (s *Service) publishNewSite(ctx context.Context, a Actor, title, slug, upload string,
	source domain.DeploySource, meta map[string]any) (DeployResult, error) {
	v, err := s.CreateSite(ctx, a, title, slug)
	if err != nil {
		return DeployResult{}, err
	}
	res, err := s.publishFirst(ctx, a, v.Site, upload, source, meta)
	if err != nil {
		if delErr := s.softDelete(context.WithoutCancel(ctx), a, v.Site, "site.delete"); delErr != nil {
			s.log.Error("remove site after failed publication", "site", v.Site.ID, "err", delErr)
		}
		return DeployResult{}, err
	}
	return res, nil
}

func (s *Service) publishFirst(ctx context.Context, a Actor, site domain.Site, upload string,
	source domain.DeploySource, meta map[string]any) (DeployResult, error) {
	lim, err := s.siteOwnerLimits(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	unlock := s.siteLocks.lock(site.ID)
	defer unlock()
	d, err := s.publish(ctx, a, site, upload, lim, source)
	if err != nil {
		return DeployResult{}, err
	}
	m := map[string]any{"deploy": d.ID, "source": source, "files": d.FileCount, "bytes": d.SizeBytes}
	for k, v := range meta {
		m[k] = v
	}
	s.audit(ctx, a, "deploy.create", siteTarget(site.ID), m)
	if site, err = s.repo.GetSite(ctx, site.ID); err != nil {
		return DeployResult{}, err
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return DeployResult{}, err
	}
	return DeployResult{Deploy: d, Site: v}, nil
}

// writeUpload stores data as a temporary upload file for the pipeline.
func (s *Service) writeUpload(data []byte) (string, error) {
	f, err := os.CreateTemp(s.uploadDir, "upload-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(name)
		return "", werr
	}
	return name, nil
}
