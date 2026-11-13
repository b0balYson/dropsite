package stats

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// EndCursor returns the cursor at the current end of the log, for readers
// that only care about lines written from now on (the ban detector). A
// missing log yields the zero Cursor.
func EndCursor(path string) (Cursor, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Cursor{}, nil
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("stats: stat log: %w", err)
	}
	ino, ok := inodeOf(st)
	if !ok {
		return Cursor{}, ErrNoInode
	}
	return Cursor{Inode: ino, Offset: st.Size()}, nil
}
