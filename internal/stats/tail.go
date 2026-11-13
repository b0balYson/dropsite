package stats

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaxLineBytes bounds one access-log line; longer lines are skipped.
const MaxLineBytes = 64 << 10

// Cursor is the read position in the access log: the file's inode and the
// offset just past the last consumed line. The zero Cursor means "start of
// whatever file is current".
type Cursor struct {
	Inode  uint64
	Offset int64
}

// Chunk is the result of one ReadLog call.
type Chunk struct {
	Lines   [][]byte
	Next    Cursor
	Skipped int  // over-long lines dropped
	Lost    bool // the file the cursor pointed to was rotated away unread
}

// Done reports whether the call made no progress: the caller has caught up.
func (c Chunk) Done(prev Cursor) bool {
	return len(c.Lines) == 0 && c.Skipped == 0 && c.Next == prev
}

// ErrNoInode is returned on platforms without inode numbers.
var ErrNoInode = errors.New("stats: file identity (inode) is not available on this platform")

// ReadLog returns complete lines after cursor c, reading at most maxBytes.
//
// Rotation (Caddy renames the file to <stem>-<time>.log and starts a new
// one) is detected by a changed inode: the rest of the old file is read from
// the rotated copy, found by its inode, and the next call continues with the
// new file from the start. A file shorter than the offset was truncated and is
// read from the start. A trailing line without a newline is left for the next
// call unless the file is a rotated one, which no longer grows. A missing log
// is not an error: there is nothing to read yet.
func ReadLog(path string, c Cursor, maxBytes int64) (Chunk, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Chunk{Next: c}, nil
	}
	if err != nil {
		return Chunk{}, fmt.Errorf("stats: stat log: %w", err)
	}
	ino, ok := inodeOf(st)
	if !ok {
		return Chunk{}, ErrNoInode
	}
	if c.Inode != 0 && c.Inode != ino {
		return readRotated(path, c, ino, maxBytes)
	}
	offset := c.Offset
	if c.Inode == 0 || st.Size() < offset {
		offset = 0
	}
	replaced := false
	if offset > 0 {
		var err error
		if replaced, err = replacedUnder(path, offset); err != nil {
			return Chunk{}, err
		}
		if replaced {
			offset = 0
		}
	}
	ch, err := readFrom(path, Cursor{Inode: ino, Offset: offset}, st.Size(), maxBytes, false)
	ch.Lost = ch.Lost || replaced
	return ch, err
}

// replacedUnder reports whether the file at path is not the one the cursor
// was read from although it has the same inode: file systems reuse the inode
// of a deleted log for the next file. The cursor always stops right after a
// newline, or inside a line longer than MaxLineBytes that was skipped, so any
// other byte before the offset means the content changed under it.
func replacedUnder(path string, offset int64) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from configuration
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stats: open log: %w", err)
	}
	defer f.Close()
	window := min(offset, int64(MaxLineBytes)+1)
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, offset-window); err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("stats: read log: %w", err)
	}
	if buf[len(buf)-1] == '\n' {
		return false, nil
	}
	// Inside a skipped over-long line: no newline in the last MaxLineBytes+1 bytes.
	return window <= int64(MaxLineBytes) || bytes.IndexByte(buf, '\n') >= 0, nil
}

func readRotated(path string, c Cursor, current uint64, maxBytes int64) (Chunk, error) {
	old, size, err := findByInode(path, c.Inode)
	if err != nil {
		return Chunk{}, err
	}
	if old == "" || size <= c.Offset {
		return Chunk{Next: Cursor{Inode: current}, Lost: old == ""}, nil
	}
	ch, err := readFrom(old, c, size, maxBytes, true)
	if err != nil {
		return Chunk{}, err
	}
	if ch.Next.Offset >= size {
		ch.Next = Cursor{Inode: current}
	}
	return ch, nil
}

// findByInode looks for a rotated sibling of path (<stem>-*<ext>) with the
// given inode and returns its name and size, or "" if it is gone.
func findByInode(path string, ino uint64) (string, int64, error) {
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(filepath.Base(path), ext) + "-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("stats: list log directory: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, stem) || !strings.HasSuffix(name, ext) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed meanwhile
		}
		if n, ok := inodeOf(info); ok && n == ino {
			return filepath.Join(dir, name), info.Size(), nil
		}
	}
	return "", 0, nil
}

func readFrom(path string, c Cursor, size, maxBytes int64, final bool) (Chunk, error) {
	n := min(size-c.Offset, maxBytes)
	if n <= 0 {
		return Chunk{Next: c}, nil
	}
	f, err := os.Open(path) //nolint:gosec // path comes from configuration and its own directory listing
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Chunk{Next: c}, nil
		}
		return Chunk{}, fmt.Errorf("stats: open log: %w", err)
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, c.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return Chunk{}, fmt.Errorf("stats: read log: %w", err)
	}
	buf = buf[:read]
	atEOF := c.Offset+int64(read) >= size
	return splitLines(buf, c, final && atEOF), nil
}

// splitLines cuts buf into lines. When the buffer holds no newline at all
// and is not the end of a final file, the whole buffer is one over-long line
// fragment and is skipped so reading can move on.
func splitLines(buf []byte, c Cursor, final bool) Chunk {
	ch := Chunk{Next: c}
	consumed := 0
	for {
		i := bytes.IndexByte(buf[consumed:], '\n')
		if i < 0 {
			break
		}
		ch.add(buf[consumed : consumed+i])
		consumed += i + 1
	}
	rest := buf[consumed:]
	switch {
	case len(rest) == 0:
	case final:
		ch.add(rest)
		consumed = len(buf)
	case consumed == 0 && len(rest) > MaxLineBytes:
		ch.Skipped++
		consumed = len(buf)
	}
	ch.Next.Offset += int64(consumed)
	return ch
}

func (c *Chunk) add(line []byte) {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	switch {
	case len(line) == 0:
	case len(line) > MaxLineBytes:
		c.Skipped++
	default:
		c.Lines = append(c.Lines, line)
	}
}
