package storage

import (
	"errors"
	"fmt"
	"io"
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

// CopyResult is the size of a copied release.
type CopyResult struct {
	Bytes int64
	Files int
}

// CopyRelease copies releases/<fromID> into the staging directory of toID
// (releases/<toID>.tmp), keeping modes and modification times so unchanged
// files keep their ETags. Only directories and regular files are copied;
// releases contain nothing else. The caller commits or discards the staging
// directory as after an extraction.
func (f *FS) CopyRelease(siteID, fromID, toID string) (CopyResult, error) {
	if err := validateSiteAndDeploy(siteID, fromID); err != nil {
		return CopyResult{}, err
	}
	dest, err := f.StagingDir(siteID, toID)
	if err != nil {
		return CopyResult{}, err
	}
	src := f.releasePath(siteID, fromID)
	if ok, err := isDir(src); err != nil {
		return CopyResult{}, fmt.Errorf("storage: stat release: %w", err)
	} else if !ok {
		return CopyResult{}, fmt.Errorf("%w: release %s/%s", ErrNotFound, siteID, fromID)
	}
	if _, err := os.Lstat(dest); err == nil {
		return CopyResult{}, fmt.Errorf("%w: staging %s/%s", ErrExists, siteID, toID)
	}
	res, err := copyTree(src, dest)
	if err != nil {
		_ = os.RemoveAll(dest)
		return CopyResult{}, fmt.Errorf("storage: copy release: %w", err)
	}
	return res, nil
}

func copyTree(src, dest string) (CopyResult, error) {
	from, err := os.OpenRoot(src)
	if err != nil {
		return CopyResult{}, err
	}
	defer from.Close()
	if err := os.Mkdir(dest, dirPerm); err != nil {
		return CopyResult{}, err
	}
	to, err := os.OpenRoot(dest)
	if err != nil {
		return CopyResult{}, err
	}
	defer to.Close()
	var res CopyResult
	var dirs []string
	err = fs.WalkDir(from.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		name := filepath.FromSlash(p)
		switch {
		case d.IsDir():
			dirs = append(dirs, name)
			return to.Mkdir(name, dirPerm)
		case d.Type().IsRegular():
			n, err := copyFile(from, to, name)
			res.Bytes += n
			res.Files++
			return err
		default:
			return fmt.Errorf("unexpected file type at %s", p)
		}
	})
	if err != nil {
		return CopyResult{}, err
	}
	// Directory times last: creating entries updates them.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := copyTimes(from, to, dirs[i]); err != nil {
			return CopyResult{}, err
		}
	}
	return res, nil
}

func copyFile(from, to *os.Root, name string) (int64, error) {
	in, err := from.Open(name)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, err
	}
	out, err := to.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, in)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return n, err
	}
	if err := to.Chmod(name, info.Mode().Perm()); err != nil {
		return n, err
	}
	return n, to.Chtimes(name, info.ModTime(), info.ModTime())
}

func copyTimes(from, to *os.Root, name string) error {
	info, err := from.Stat(name)
	if err != nil {
		return err
	}
	return to.Chtimes(name, info.ModTime(), info.ModTime())
}

// OpenRelease opens releases/<deployID> for reading. Every access through the
// returned root stays inside the release directory. The caller closes it.
func (f *FS) OpenRelease(siteID, deployID string) (*os.Root, error) {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return nil, err
	}
	dir := f.releasePath(siteID, deployID)
	if ok, err := isDir(dir); err != nil {
		return nil, fmt.Errorf("storage: stat release: %w", err)
	} else if !ok {
		return nil, fmt.Errorf("%w: release %s/%s", ErrNotFound, siteID, deployID)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: open release: %w", err)
	}
	return root, nil
}
