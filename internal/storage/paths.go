// Package storage manages the on-disk layout of published sites:
//
//	<root>/sites/<site_id>/releases/<deploy_id>/      release files
//	<root>/sites/<site_id>/releases/<deploy_id>.tmp/  extraction in progress
//	<root>/sites/<site_id>/current -> releases/<deploy_id> | ../../empty
//	<root>/by-host/<hostname> -> ../sites/<site_id>/current
//	<root>/by-host/<preview>  -> ../sites/<site_id>/releases/<deploy_id>
//	<root>/empty/                                      shared empty directory
//	<root>/flags/<flag>/<hostname>                     per-host marker files
//
// Caddy mounts the root read-only, possibly at another path, so every link is
// relative. Every link replacement is atomic (temporary link + rename(2)).
package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const (
	sitesDir    = "sites"
	byHostDir   = "by-host"
	emptyDir    = "empty"
	releasesDir = "releases"
	currentLink = "current"
	stagingExt  = ".tmp"
	dirPerm     = 0o755

	// emptyTarget is the current target of a site without releases,
	// relative to sites/<id>/.
	emptyTarget = "../../" + emptyDir
)

// Storage is the file-system side of publishing.
type Storage interface {
	Init(ctx context.Context) error
	StagingDir(siteID, deployID string) (string, error)
	CommitRelease(siteID, deployID string) error
	CopyRelease(siteID, fromID, toID string) (CopyResult, error)
	DiscardStaging(siteID, deployID string) error
	ActivateRelease(siteID, deployID string) error
	ActivateEmpty(siteID string) error
	LinkHost(hostname, siteID string) error
	LinkPreview(hostname, siteID, deployID string) error
	LinkDraft(siteID string, deployID *string) error
	LinkDraftHost(hostname, siteID string) error
	UnlinkHost(hostname string) error
	SetHostFlag(flag HostFlag, hostname string) error
	ClearHostFlag(flag HostFlag, hostname string) error
	DeleteRelease(siteID, deployID string) error
	DeleteSite(siteID string) error
	Reconcile(ctx context.Context, desired []SiteLayout, keep Retained) (ReconcileReport, error)
	SiteSize(siteID, deployID string) (int64, error)
	OpenRelease(siteID, deployID string) (*os.Root, error)
}

// FS is the local file-system implementation of Storage.
type FS struct {
	root string
}

var _ Storage = (*FS)(nil)

// NewFS returns a driver rooted at root (for example /data).
func NewFS(root string) *FS {
	return &FS{root: filepath.Clean(root)}
}

// Init creates sites/, by-host/, empty/ and the flag directories if missing.
func (f *FS) Init(ctx context.Context) error {
	dirs := []string{sitesDir, byHostDir, emptyDir}
	for _, fl := range AllHostFlags {
		dirs = append(dirs, filepath.Join(flagsDir, string(fl)))
	}
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(f.root, d), dirPerm); err != nil {
			return fmt.Errorf("storage: init %s: %w", d, err)
		}
	}
	return nil
}

func (f *FS) sitesPath() string  { return filepath.Join(f.root, sitesDir) }
func (f *FS) byHostPath() string { return filepath.Join(f.root, byHostDir) }

func (f *FS) sitePath(siteID string) string {
	return filepath.Join(f.root, sitesDir, siteID)
}

func (f *FS) releasesPath(siteID string) string {
	return filepath.Join(f.sitePath(siteID), releasesDir)
}

func (f *FS) releasePath(siteID, deployID string) string {
	return filepath.Join(f.releasesPath(siteID), deployID)
}

func (f *FS) stagingPath(siteID, deployID string) string {
	return f.releasePath(siteID, deployID) + stagingExt
}

func (f *FS) currentPath(siteID string) string {
	return filepath.Join(f.sitePath(siteID), currentLink)
}

func (f *FS) hostPath(hostname string) string {
	return filepath.Join(f.byHostPath(), hostname)
}

// releaseTarget is the current target for a release, relative to sites/<id>/.
func releaseTarget(deployID string) string {
	return releasesDir + "/" + deployID
}

// hostTarget is the by-host target for a site, relative to by-host/.
func hostTarget(siteID string) string {
	return "../" + sitesDir + "/" + siteID + "/" + currentLink
}

// previewTarget is the by-host target of a release preview, relative to
// by-host/: the release itself, so a rollback does not change the preview.
func previewTarget(siteID, deployID string) string {
	return "../" + sitesDir + "/" + siteID + "/" + releasesDir + "/" + deployID
}

// isDir reports whether p exists and is a real directory (not a symlink).
func isDir(p string) (bool, error) {
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}
