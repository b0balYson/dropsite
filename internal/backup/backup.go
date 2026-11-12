// Package backup writes and restores a single tar.gz with a consistent
// database snapshot and the sites volume (releases and relative symlinks).
//
// Order matters: the database is snapshotted first, then the files are read.
// A deploy that lands in between leaves an extra release directory on disk,
// which reconciliation after a restore removes; the database stays the
// source of truth.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/store"
)

const (
	prefix       = "dropsite-backup/"
	manifestName = prefix + "manifest.json"
	dbName       = prefix + "state.db"
	sitesPrefix  = prefix + "sites/"
	formatV1     = 1
)

// Manifest describes a backup.
type Manifest struct {
	Format        int       `json:"format"`
	Product       string    `json:"product"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// Backup writes the archive to w. tmpDir must be on a writable volume.
func Backup(ctx context.Context, db *store.DB, sitesDir, tmpDir string, w io.Writer) (Manifest, error) {
	snap, err := os.CreateTemp(tmpDir, "backup-*.db")
	if err != nil {
		return Manifest{}, fmt.Errorf("create snapshot file: %w", err)
	}
	snapPath := snap.Name()
	_ = snap.Close()
	_ = os.Remove(snapPath) // VACUUM INTO requires the target not to exist
	defer os.Remove(snapPath)
	if _, err := db.Writer().ExecContext(ctx, "VACUUM INTO ?", snapPath); err != nil {
		return Manifest{}, fmt.Errorf("snapshot database: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	m := Manifest{Format: formatV1, Product: brand.Name, Version: brand.Version,
		SchemaVersion: store.LatestVersion(), CreatedAt: time.Now().UTC()}
	if err := writeJSON(tw, manifestName, m); err != nil {
		return Manifest{}, err
	}
	if err := writeFile(tw, dbName, snapPath, 0o600); err != nil {
		return Manifest{}, err
	}
	if err := writeTree(ctx, tw, sitesDir); err != nil {
		return Manifest{}, err
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return Manifest{}, fmt.Errorf("finish archive: %w", err)
	}
	return m, nil
}

func writeJSON(tw *tar.Writer, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: time.Now(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	_, err = tw.Write(b)
	return err
}

func writeFile(tw *tar.Writer, name, path string, mode int64) error {
	f, err := os.Open(path) //nolint:gosec // paths come from walking our own volume
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	hdr := &tar.Header{Name: name, Mode: mode, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write header %s: %w", name, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// writeTree archives the sites volume; symlinks are stored as links, never followed.
func writeTree(ctx context.Context, tw *tar.Writer, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		name := sitesPrefix + filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read link %s: %w", path, err)
			}
			return tw.WriteHeader(&tar.Header{Name: name, Linkname: target, Typeflag: tar.TypeSymlink, Mode: 0o777, ModTime: info.ModTime()})
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: info.ModTime()})
		case info.Mode().IsRegular():
			return writeFile(tw, name, path, 0o644)
		default:
			return nil // sockets, devices: never produced by control
		}
	})
}

// isInside reports whether rel (slash-separated, relative) stays inside its root.
func isInside(rel string) bool {
	return rel != "" && filepath.IsLocal(filepath.FromSlash(rel))
}

// linkTargetInside reports whether a symlink at rel pointing to target stays
// inside the sites root.
func linkTargetInside(rel, target string) bool {
	if target == "" || strings.HasPrefix(target, "/") || filepath.IsAbs(target) {
		return false
	}
	joined := filepath.Join(filepath.Dir(filepath.FromSlash(rel)), filepath.FromSlash(target))
	return filepath.IsLocal(joined)
}
