package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StagingDir creates sites/<siteID>/releases if needed and returns the path
// of releases/<deployID>.tmp. The staging directory itself is not created:
// the archive extractor creates it.
func (f *FS) StagingDir(siteID, deployID string) (string, error) {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return "", err
	}
	if err := os.MkdirAll(f.releasesPath(siteID), dirPerm); err != nil {
		return "", fmt.Errorf("storage: create releases dir: %w", err)
	}
	return f.stagingPath(siteID, deployID), nil
}

// CommitRelease renames releases/<deployID>.tmp to releases/<deployID>.
func (f *FS) CommitRelease(siteID, deployID string) error {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return err
	}
	staging := f.stagingPath(siteID, deployID)
	ok, err := isDir(staging)
	if err != nil {
		return fmt.Errorf("storage: stat staging: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: staging %s/%s", ErrNotFound, siteID, deployID)
	}
	final := f.releasePath(siteID, deployID)
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("%w: release %s/%s", ErrExists, siteID, deployID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("storage: stat release: %w", err)
	}
	if err := os.Rename(staging, final); err != nil {
		return fmt.Errorf("storage: commit release: %w", err)
	}
	return nil
}

// DiscardStaging removes releases/<deployID>.tmp; a missing one is not an error.
func (f *FS) DiscardStaging(siteID, deployID string) error {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return err
	}
	if err := os.RemoveAll(f.stagingPath(siteID, deployID)); err != nil {
		return fmt.Errorf("storage: discard staging: %w", err)
	}
	return nil
}

// DeleteRelease removes releases/<deployID>. It refuses to delete the release
// current points to. A missing release is not an error.
func (f *FS) DeleteRelease(siteID, deployID string) error {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return err
	}
	cur, err := os.Readlink(f.currentPath(siteID))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: read current: %w", err)
	}
	if err == nil && filepath.Clean(cur) == releaseTarget(deployID) {
		return fmt.Errorf("%w: %s/%s", ErrCurrentRelease, siteID, deployID)
	}
	if err := os.RemoveAll(f.releasePath(siteID, deployID)); err != nil {
		return fmt.Errorf("storage: delete release: %w", err)
	}
	return nil
}

// DeleteSite removes sites/<siteID> entirely. by-host links are left to the
// caller (UnlinkHost). A missing site is not an error.
func (f *FS) DeleteSite(siteID string) error {
	if err := validateSiteID(siteID); err != nil {
		return err
	}
	if err := os.RemoveAll(f.sitePath(siteID)); err != nil {
		return fmt.Errorf("storage: delete site: %w", err)
	}
	return nil
}

// SiteSize returns the total size in bytes of regular files in a release.
// Symlinks are not followed.
func (f *FS) SiteSize(siteID, deployID string) (int64, error) {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return 0, err
	}
	dir := f.releasePath(siteID, deployID)
	ok, err := isDir(dir)
	if err != nil {
		return 0, fmt.Errorf("storage: stat release: %w", err)
	}
	if !ok {
		return 0, fmt.Errorf("%w: release %s/%s", ErrNotFound, siteID, deployID)
	}
	var total int64
	err = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("storage: measure release: %w", err)
	}
	return total, nil
}
