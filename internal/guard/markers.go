package guard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Marker files under the sites volume that Caddy checks with "file"
// matchers (Caddy mounts the volume read-only and is never reloaded).
const (
	// FlagDefenderOff turns the scraper and cloud blocking off.
	FlagDefenderOff = "defender-off"
	// FlagDefenderStrict blocks cloud ranges on whole sites, not only on
	// the secret proxy.
	FlagDefenderStrict = "defender-strict"

	flagsDir   = "flags"
	bansDir    = "bans"
	dirPerm    = 0o755
	markerPerm = 0o644
	tempPrefix = ".tmpmark-"
)

// ErrBadMarker is returned for a marker name that is not allowed.
var ErrBadMarker = errors.New("guard: invalid marker name")

// Markers writes and removes marker files below root (/data in control).
type Markers struct{ root string }

// NewMarkers returns a writer rooted at root.
func NewMarkers(root string) *Markers { return &Markers{root: filepath.Clean(root)} }

func validFlag(name string) bool { return name == FlagDefenderOff || name == FlagDefenderStrict }

// SetFlag creates (on) or removes the global flag marker name.
func (m *Markers) SetFlag(name string, on bool) error {
	if !validFlag(name) {
		return fmt.Errorf("%w: %q", ErrBadMarker, name)
	}
	dir := filepath.Join(m.root, flagsDir)
	if on {
		return writeMarker(dir, name)
	}
	return removeMarker(dir, name)
}

// FlagSet reports whether the flag marker name exists.
func (m *Markers) FlagSet(name string) bool {
	if !validFlag(name) {
		return false
	}
	fi, err := os.Lstat(filepath.Join(m.root, flagsDir, name))
	return err == nil && fi.Mode().IsRegular()
}

// Ban creates the ban marker of one client address (canonical form).
func (m *Markers) Ban(addr string) error {
	a := CanonicalIP(addr)
	if a == "" || a != addr {
		return fmt.Errorf("%w: %q", ErrBadMarker, addr)
	}
	return writeMarker(filepath.Join(m.root, bansDir), a)
}

// Unban removes the ban marker of one address; a missing one is no error.
func (m *Markers) Unban(addr string) error {
	a := CanonicalIP(addr)
	if a == "" {
		return fmt.Errorf("%w: %q", ErrBadMarker, addr)
	}
	return removeMarker(filepath.Join(m.root, bansDir), a)
}

// Banned lists the addresses that have a ban marker.
func (m *Markers) Banned() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(m.root, bansDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("guard: read bans: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			_ = os.Remove(filepath.Join(m.root, bansDir, e.Name()))
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// writeMarker creates dir/name as an empty regular file readable by Caddy
// whatever the umask (temporary file plus rename).
func writeMarker(dir, name string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("guard: create %s: %w", dir, err)
	}
	p := filepath.Join(dir, name)
	if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
		return nil
	}
	tmp, err := os.CreateTemp(dir, tempPrefix)
	if err != nil {
		return fmt.Errorf("guard: create marker: %w", err)
	}
	tn := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tn)
		return fmt.Errorf("guard: create marker: %w", err)
	}
	if err := os.Chmod(tn, markerPerm); err != nil {
		_ = os.Remove(tn)
		return fmt.Errorf("guard: chmod marker: %w", err)
	}
	if err := os.Rename(tn, p); err != nil {
		_ = os.Remove(tn)
		return fmt.Errorf("guard: set marker %s: %w", name, err)
	}
	return nil
}

func removeMarker(dir, name string) error {
	if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("guard: remove marker %s: %w", name, err)
	}
	return nil
}
