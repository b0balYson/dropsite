//go:build unix

package stats

import (
	"io/fs"
	"syscall"
)

func inodeOf(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Ino), true //nolint:unconvert // Ino is not uint64 on every unix
}
