package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b0balYson/dropsite/internal/storage"
	"github.com/b0balYson/dropsite/internal/store"
)

// ErrNotEmpty is returned when restoring over existing data without force.
var ErrNotEmpty = errors.New("the installation already has data; pass --force to replace it")

// maxManifestBytes bounds the manifest read.
const maxManifestBytes = 1 << 16

// Restore replaces the database and the sites volume with the archive read
// from r. The server must be stopped. Without force it refuses to overwrite
// an installation that already has a database or sites.
func Restore(ctx context.Context, r io.Reader, stateDir, sitesDir string, force bool) (Manifest, error) {
	dbPath := filepath.Join(stateDir, "state.db")
	if !force {
		if nonEmpty, err := hasData(dbPath, sitesDir); err != nil {
			return Manifest{}, err
		} else if nonEmpty {
			return Manifest{}, ErrNotEmpty
		}
	}
	stage, err := os.MkdirTemp(stateDir, "restore-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	m, err := extract(ctx, r, stage)
	if err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion > store.LatestVersion() {
		return Manifest{}, fmt.Errorf("backup schema %d is newer than this binary (%d)", m.SchemaVersion, store.LatestVersion())
	}
	if err := replaceDB(filepath.Join(stage, "state.db"), dbPath); err != nil {
		return Manifest{}, err
	}
	if err := replaceSites(filepath.Join(stage, "sites"), sitesDir); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func hasData(dbPath, sitesDir string) (bool, error) {
	if _, err := os.Stat(dbPath); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("check database: %w", err)
	}
	entries, err := os.ReadDir(filepath.Join(sitesDir, "sites"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check sites: %w", err)
	}
	return len(entries) > 0, nil
}

func extract(ctx context.Context, r io.Reader, stage string) (Manifest, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Manifest{}, fmt.Errorf("not a backup archive: %w", err)
	}
	defer gz.Close()
	sw, err := newSiteWriter(stage)
	if err != nil {
		return Manifest{}, err
	}
	defer sw.close()
	tr := tar.NewReader(gz)
	var m Manifest
	sawDB := false
	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read backup: %w", err)
		}
		switch {
		case hdr.Name == manifestName:
			if err := json.NewDecoder(io.LimitReader(tr, maxManifestBytes)).Decode(&m); err != nil {
				return Manifest{}, fmt.Errorf("read manifest: %w", err)
			}
		case hdr.Name == dbName:
			if err := copyTo(filepath.Join(stage, "state.db"), tr, 0o600); err != nil {
				return Manifest{}, err
			}
			sawDB = true
		case strings.HasPrefix(hdr.Name, sitesPrefix):
			if err := sw.extract(hdr, tr); err != nil {
				return Manifest{}, err
			}
		default:
			return Manifest{}, fmt.Errorf("unexpected entry %q in backup", hdr.Name)
		}
	}
	if m.Format != formatV1 || !sawDB {
		return Manifest{}, errors.New("archive is not a complete Dropsite backup")
	}
	return m, nil
}

// siteWriter materializes the sites part of a backup inside an os.Root, so no
// entry can reach outside the staging directory even through links, and
// remembers the links it created so nothing is ever written through one.
type siteWriter struct {
	root  *os.Root
	links map[string]bool
}

func newSiteWriter(stage string) (*siteWriter, error) {
	dir := filepath.Join(stage, "sites")
	if err := os.Mkdir(dir, 0o755); err != nil { //nolint:gosec // readable by Caddy
		return nil, fmt.Errorf("create staging sites directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open staging sites directory: %w", err)
	}
	return &siteWriter{root: root, links: map[string]bool{}}, nil
}

func (w *siteWriter) close() { _ = w.root.Close() }

func (w *siteWriter) extract(hdr *tar.Header, tr io.Reader) error {
	rel := strings.TrimSuffix(strings.TrimPrefix(hdr.Name, sitesPrefix), "/")
	if rel == "" {
		return nil
	}
	if !isInside(rel) {
		return fmt.Errorf("unsafe path %q in backup", hdr.Name)
	}
	if w.atOrBelowLink(rel) {
		return fmt.Errorf("entry %q in backup is at or below a link", hdr.Name)
	}
	name := filepath.FromSlash(rel)
	if dir := filepath.Dir(name); dir != "." {
		if err := w.root.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // readable by Caddy
			return fmt.Errorf("create directory: %w", err)
		}
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return w.root.MkdirAll(name, 0o755) //nolint:gosec // readable by Caddy
	case tar.TypeReg:
		f, err := w.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // readable by Caddy
		if err != nil {
			return fmt.Errorf("create %s: %w", rel, err)
		}
		return writeAndClose(f, tr, rel)
	case tar.TypeSymlink:
		if !allowedLink(rel, hdr.Linkname) {
			return fmt.Errorf("unexpected link %q -> %q in backup", hdr.Name, hdr.Linkname)
		}
		if err := w.root.Symlink(hdr.Linkname, name); err != nil {
			return fmt.Errorf("create link %s: %w", rel, err)
		}
		w.links[rel] = true
		return nil
	default:
		return fmt.Errorf("unsupported entry type for %q", hdr.Name)
	}
}

// atOrBelowLink reports whether rel or one of its parents is a restored link.
func (w *siteWriter) atOrBelowLink(rel string) bool {
	for i := range len(rel) {
		if rel[i] == '/' && w.links[rel[:i]] {
			return true
		}
	}
	return w.links[rel]
}

// allowedLink is the strict whitelist of links a backup may contain: exactly
// the ones internal/storage creates, with well-formed IDs and hostnames.
//
//	sites/<site_id>/current -> releases/<deploy_id> | ../../empty
//	by-host/<hostname>      -> ../sites/<site_id>/current
func allowedLink(rel, target string) bool {
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) == 3 && parts[0] == "sites" && parts[2] == "current" && storage.ValidSiteID(parts[1]):
		return storage.CurrentTargetValid(target)
	case len(parts) == 2 && parts[0] == "by-host" && storage.ValidHostname(parts[1]):
		return storage.HostTargetValid(target)
	}
	return false
}

func writeAndClose(f *os.File, r io.Reader, name string) error {
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	return f.Close()
}

func copyTo(dst string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) //nolint:gosec // staging and sites paths built by this package
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	return writeAndClose(f, r, dst)
}

func replaceDB(src, dbPath string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", dbPath+suffix, err)
		}
	}
	if err := os.Rename(src, dbPath); err != nil {
		return fmt.Errorf("install database: %w", err)
	}
	return nil
}

// replaceSites empties sitesDir (which may be a mount point and cannot be
// removed itself) and moves the restored tree in.
func replaceSites(src, sitesDir string) error {
	if err := os.MkdirAll(src, 0o755); err != nil { //nolint:gosec // readable by Caddy
		return fmt.Errorf("prepare sites: %w", err)
	}
	if err := os.MkdirAll(sitesDir, 0o755); err != nil { //nolint:gosec // readable by Caddy
		return fmt.Errorf("create sites directory: %w", err)
	}
	old, err := os.ReadDir(sitesDir)
	if err != nil {
		return fmt.Errorf("read sites directory: %w", err)
	}
	for _, e := range old {
		if err := os.RemoveAll(filepath.Join(sitesDir, e.Name())); err != nil {
			return fmt.Errorf("clear sites directory: %w", err)
		}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read restored sites: %w", err)
	}
	for _, e := range entries {
		if err := moveOrCopy(filepath.Join(src, e.Name()), filepath.Join(sitesDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// moveOrCopy renames, falling back to a copy across filesystems (state and
// sites are separate volumes).
func moveOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyTreeWithLinks(src, dst)
}

func copyTreeWithLinks(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read link: %w", err)
			}
			return os.Symlink(link, target) //nolint:gosec // walking our own staging directory; links are recreated, not followed
		case d.IsDir():
			return os.MkdirAll(target, 0o755) //nolint:gosec // readable by Caddy
		default:
			f, err := os.Open(path) //nolint:gosec // walking our own staging directory, links are recreated not followed
			if err != nil {
				return fmt.Errorf("open %s: %w", path, err)
			}
			defer f.Close()
			return copyTo(target, f, 0o644)
		}
	})
}
