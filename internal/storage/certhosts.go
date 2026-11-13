package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// certHostsDir holds one empty marker file per host name that is not a site
// address but may still get an on-demand certificate: Caddy's permission
// endpoint allows <name> when <root>/tls-hosts/<name> is a regular file.
// Today the only such name is the public admin host (admin.<base domain>),
// present only while the administrator keeps public access on.
const certHostsDir = "tls-hosts"

func (f *FS) certHostsPath() string { return filepath.Join(f.root, certHostsDir) }

// SetCertHosts makes tls-hosts/ hold exactly the markers of hosts: missing
// ones are created, every other entry is removed. The database is the
// source of truth, so calling it again with the same list changes nothing.
func (f *FS) SetCertHosts(ctx context.Context, hosts []string) error {
	for _, h := range hosts {
		if err := validateHostname(h); err != nil {
			return err
		}
	}
	dir := f.certHostsPath()
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("storage: create %s: %w", certHostsDir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("storage: read %s: %w", certHostsDir, err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if slices.Contains(hosts, e.Name()) && e.Type().IsRegular() {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("storage: remove %s/%s: %w", certHostsDir, e.Name(), err)
		}
	}
	for _, h := range hosts {
		if err := writeMarker(dir, h); err != nil {
			return err
		}
	}
	return nil
}

// writeMarker creates dir/name as an empty regular file (temporary file plus
// rename), readable by Caddy whatever the umask.
func writeMarker(dir, name string) error {
	p := filepath.Join(dir, name)
	if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
		return nil
	}
	tmp, err := os.CreateTemp(dir, tempFlagPrefix)
	if err != nil {
		return fmt.Errorf("storage: create marker: %w", err)
	}
	name0 := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name0)
		return fmt.Errorf("storage: create marker: %w", err)
	}
	if err := os.Chmod(name0, flagPerm); err != nil {
		_ = os.Remove(name0)
		return fmt.Errorf("storage: chmod marker: %w", err)
	}
	if err := os.Rename(name0, p); err != nil {
		_ = os.Remove(name0)
		return fmt.Errorf("storage: set marker %s: %w", name, err)
	}
	return nil
}
