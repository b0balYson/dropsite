package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostFlag names a per-host marker directory under <root>/flags/. Caddy
// checks <root>/flags/<flag>/<hostname> with a file matcher, so a marker is
// an empty regular file.
type HostFlag string

const (
	// FlagNoindex makes Caddy add X-Robots-Tag: noindex, nofollow.
	FlagNoindex HostFlag = "noindex"
	// FlagProtected makes Caddy ask control (forward_auth) before serving.
	FlagProtected HostFlag = "protected"
)

// AllHostFlags lists every flag in a stable order.
var AllHostFlags = []HostFlag{FlagNoindex, FlagProtected}

const (
	flagsDir       = "flags"
	flagPerm       = 0o644
	tempFlagPrefix = ".tmpflag-"
)

func validateFlag(flag HostFlag) error {
	for _, f := range AllHostFlags {
		if f == flag {
			return nil
		}
	}
	return fmt.Errorf("%w: flag %q", ErrInvalidName, flag)
}

func (f *FS) flagDir(flag HostFlag) string { return filepath.Join(f.root, flagsDir, string(flag)) }

func (f *FS) flagPath(flag HostFlag, hostname string) string {
	return filepath.Join(f.flagDir(flag), hostname)
}

func isTempFlag(name string) bool { return strings.HasPrefix(name, tempFlagPrefix) }

// SetHostFlag creates the marker for hostname atomically (temporary file plus
// rename). Anything else in its place is replaced; setting twice is a no-op.
func (f *FS) SetHostFlag(flag HostFlag, hostname string) error {
	if err := validateFlag(flag); err != nil {
		return err
	}
	if err := validateHostname(hostname); err != nil {
		return err
	}
	_, err := f.ensureFlag(flag, hostname)
	return err
}

// ensureFlag makes the marker a regular file and reports whether it changed.
func (f *FS) ensureFlag(flag HostFlag, hostname string) (bool, error) {
	p := f.flagPath(flag, hostname)
	fi, err := os.Lstat(p)
	switch {
	case err == nil && fi.Mode().IsRegular():
		return false, nil
	case err == nil:
		if err := os.RemoveAll(p); err != nil {
			return false, fmt.Errorf("storage: replace flag %s/%s: %w", flag, hostname, err)
		}
	case !os.IsNotExist(err):
		return false, fmt.Errorf("storage: stat flag: %w", err)
	}
	if err := os.MkdirAll(f.flagDir(flag), dirPerm); err != nil {
		return false, fmt.Errorf("storage: create flag dir: %w", err)
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return false, fmt.Errorf("storage: random flag name: %w", err)
	}
	tmp := filepath.Join(f.flagDir(flag), tempFlagPrefix+hex.EncodeToString(rnd[:]))
	fh, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, flagPerm) //nolint:gosec // name built from validated parts under the storage root
	if err != nil {
		return false, fmt.Errorf("storage: create flag: %w", err)
	}
	if err := fh.Close(); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("storage: create flag: %w", err)
	}
	// Explicit chmod: the umask must not make the marker unreadable for Caddy.
	if err := os.Chmod(tmp, flagPerm); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("storage: chmod flag: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("storage: set flag %s/%s: %w", flag, hostname, err)
	}
	return true, nil
}

// ClearHostFlag removes the marker; a missing marker is not an error.
func (f *FS) ClearHostFlag(flag HostFlag, hostname string) error {
	if err := validateFlag(flag); err != nil {
		return err
	}
	if err := validateHostname(hostname); err != nil {
		return err
	}
	if err := os.RemoveAll(f.flagPath(flag, hostname)); err != nil {
		return fmt.Errorf("storage: clear flag %s/%s: %w", flag, hostname, err)
	}
	return nil
}

// desiredFlags returns, per flag, the hostnames that must carry the marker.
func desiredFlags(desired []SiteLayout) map[HostFlag]map[string]bool {
	out := map[HostFlag]map[string]bool{}
	for _, flag := range AllHostFlags {
		out[flag] = map[string]bool{}
	}
	for _, s := range desired {
		hosts := append([]string(nil), s.Hostnames...)
		for _, p := range s.Previews {
			hosts = append(hosts, p.Hostname)
		}
		if s.DraftHost != "" {
			hosts = append(hosts, s.DraftHost)
		}
		for _, h := range hosts {
			if s.Noindex {
				out[FlagNoindex][h] = true
			}
			if s.Protected {
				out[FlagProtected][h] = true
			}
		}
	}
	return out
}

// addMissingFlags creates the markers desired but absent. Reconcile runs it
// before touching by-host links, so a host is never served unflagged.
func (f *FS) addMissingFlags(ctx context.Context, want map[HostFlag]map[string]bool, rep *ReconcileReport) error {
	for _, flag := range AllHostFlags {
		if err := os.MkdirAll(f.flagDir(flag), dirPerm); err != nil {
			return fmt.Errorf("storage: create flag dir: %w", err)
		}
		for host := range want[flag] {
			if err := ctx.Err(); err != nil {
				return err
			}
			changed, err := f.ensureFlag(flag, host)
			if err != nil {
				return err
			}
			if changed {
				rep.SetFlags = append(rep.SetFlags, string(flag)+"/"+host)
			}
		}
	}
	return nil
}

// removeExtraFlags removes stray and temporary markers. Reconcile runs it
// after by-host links are repaired, so a stray link that still serves a
// protected site keeps its marker until the link itself is gone.
func (f *FS) removeExtraFlags(ctx context.Context, want map[HostFlag]map[string]bool, rep *ReconcileReport) error {
	for _, flag := range AllHostFlags {
		entries, err := os.ReadDir(f.flagDir(flag))
		if err != nil {
			return fmt.Errorf("storage: read flags: %w", err)
		}
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := e.Name()
			if want[flag][name] {
				continue
			}
			if err := os.RemoveAll(f.flagPath(flag, name)); err != nil {
				return fmt.Errorf("storage: remove flag: %w", err)
			}
			if isTempFlag(name) {
				rep.RemovedTemp = append(rep.RemovedTemp, flagsDir+"/"+string(flag)+"/"+name)
			} else {
				rep.ClearedFlags = append(rep.ClearedFlags, string(flag)+"/"+name)
			}
		}
	}
	return nil
}
