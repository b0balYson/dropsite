package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tempLinkPrefix marks temporary links created during an atomic replacement.
// It starts with a dot, so it is never a valid hostname or site ID.
const tempLinkPrefix = ".tmplink-"

// replaceSymlink atomically points link at target: it creates a uniquely
// named temporary link in the same directory and renames it over link.
// Readers always see either the old or the new link, never a missing one.
func replaceSymlink(target, link string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("storage: refusing absolute link target %q", target)
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("storage: random link name: %w", err)
	}
	tmp := filepath.Join(filepath.Dir(link), tempLinkPrefix+hex.EncodeToString(rnd[:]))
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("storage: create link: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: replace link %s: %w", filepath.Base(link), err)
	}
	return nil
}

// ensureSymlink makes link point at target. A non-link entry in its place is
// removed first (never following links). It reports whether anything changed.
func ensureSymlink(target, link string) (bool, error) {
	fi, err := os.Lstat(link)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return false, err
	case fi.Mode()&os.ModeSymlink != 0:
		if cur, err := os.Readlink(link); err == nil && cur == target {
			return false, nil
		}
	default:
		if err := os.RemoveAll(link); err != nil {
			return false, err
		}
	}
	return true, replaceSymlink(target, link)
}

func isTempLink(name string) bool { return strings.HasPrefix(name, tempLinkPrefix) }

// ActivateRelease atomically points current at releases/<deployID>.
// The release directory must exist.
func (f *FS) ActivateRelease(siteID, deployID string) error {
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return err
	}
	ok, err := isDir(f.releasePath(siteID, deployID))
	if err != nil {
		return fmt.Errorf("storage: stat release: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: release %s/%s", ErrNotFound, siteID, deployID)
	}
	return replaceSymlink(releaseTarget(deployID), f.currentPath(siteID))
}

// ActivateEmpty atomically points current at the shared empty directory,
// creating sites/<siteID>/ if needed.
func (f *FS) ActivateEmpty(siteID string) error {
	if err := validateSiteID(siteID); err != nil {
		return err
	}
	if err := os.MkdirAll(f.sitePath(siteID), dirPerm); err != nil {
		return fmt.Errorf("storage: create site dir: %w", err)
	}
	return replaceSymlink(emptyTarget, f.currentPath(siteID))
}

// LinkHost atomically points by-host/<hostname> at sites/<siteID>/current.
func (f *FS) LinkHost(hostname, siteID string) error {
	if err := validateHostname(hostname); err != nil {
		return err
	}
	if err := validateSiteID(siteID); err != nil {
		return err
	}
	return replaceSymlink(hostTarget(siteID), f.hostPath(hostname))
}

// LinkPreview atomically points by-host/<hostname> at the release directory
// sites/<siteID>/releases/<deployID>, which must exist: no dangling entry is
// ever created. UnlinkHost removes it.
func (f *FS) LinkPreview(hostname, siteID, deployID string) error {
	if err := validateHostname(hostname); err != nil {
		return err
	}
	if err := validateSiteAndDeploy(siteID, deployID); err != nil {
		return err
	}
	ok, err := isDir(f.releasePath(siteID, deployID))
	if err != nil {
		return fmt.Errorf("storage: stat release: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: release %s/%s", ErrNotFound, siteID, deployID)
	}
	return replaceSymlink(previewTarget(siteID, deployID), f.hostPath(hostname))
}

// UnlinkHost removes by-host/<hostname>; a missing entry is not an error.
// The link itself is removed, never its target.
func (f *FS) UnlinkHost(hostname string) error {
	if err := validateHostname(hostname); err != nil {
		return err
	}
	if err := os.Remove(f.hostPath(hostname)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: unlink host: %w", err)
	}
	return nil
}
