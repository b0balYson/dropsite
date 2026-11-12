package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// drain reads until caught up and returns all lines and the final cursor.
func drain(t *testing.T, path string, c Cursor, maxBytes int64) ([]string, Cursor, Chunk) {
	t.Helper()
	var out []string
	var last Chunk
	for range 1000 {
		ch, err := ReadLog(path, c, maxBytes)
		if err != nil {
			t.Fatal(err)
		}
		last.Lost = last.Lost || ch.Lost
		last.Skipped += ch.Skipped
		for _, l := range ch.Lines {
			out = append(out, string(l))
		}
		if ch.Done(c) {
			return out, c, last
		}
		c = ch.Next
	}
	t.Fatal("ReadLog never caught up")
	return nil, c, last
}

func TestReadLogMissingFile(t *testing.T) {
	ch, err := ReadLog(filepath.Join(t.TempDir(), "none.log"), Cursor{Inode: 5, Offset: 10}, 1024)
	if err != nil || len(ch.Lines) != 0 || ch.Next != (Cursor{Inode: 5, Offset: 10}) {
		t.Fatalf("got %+v, %v", ch, err)
	}
}

func TestReadLogIncrementalAndPartialLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sites-access.log")
	appendFile(t, p, "one\ntwo\nthr")
	lines, c, _ := drain(t, p, Cursor{}, 1<<20)
	if strings.Join(lines, ",") != "one,two" {
		t.Fatalf("lines = %v", lines)
	}
	if c.Offset != 8 || c.Inode == 0 {
		t.Fatalf("cursor = %+v", c)
	}
	appendFile(t, p, "ee\r\nfour\n")
	lines, c, _ = drain(t, p, c, 1<<20)
	if strings.Join(lines, ",") != "three,four" {
		t.Fatalf("lines = %v", lines)
	}
	// A restart resumes from the stored cursor without repeating lines.
	lines, _, _ = drain(t, p, c, 1<<20)
	if len(lines) != 0 {
		t.Fatalf("lines repeated: %v", lines)
	}
}

func TestReadLogSmallChunks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sites-access.log")
	appendFile(t, p, "aaaa\nbbbb\ncccc\n")
	lines, _, _ := drain(t, p, Cursor{}, 6)
	if strings.Join(lines, ",") != "aaaa,bbbb,cccc" {
		t.Fatalf("lines = %v", lines)
	}
}

func TestReadLogTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sites-access.log")
	appendFile(t, p, "old-1\nold-2\n")
	_, c, _ := drain(t, p, Cursor{}, 1<<20)
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p, "new\n")
	lines, _, _ := drain(t, p, c, 1<<20)
	if strings.Join(lines, ",") != "new" {
		t.Fatalf("lines = %v", lines)
	}
}

func TestReadLogRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sites-access.log")
	appendFile(t, p, "a\nb\n")
	_, c, _ := drain(t, p, Cursor{}, 1<<20)
	// Lines written after the last read, then Caddy rotates (rename + new
	// file), with a final line lacking its newline.
	appendFile(t, p, "c\nd")
	if err := os.Rename(p, filepath.Join(dir, "sites-access-2026-11-12T14-21-54.901-size.log")); err != nil {
		t.Fatal(err)
	}
	appendFile(t, filepath.Join(dir, "other-1.log"), "unrelated\n")
	appendFile(t, p, "e\n")
	lines, _, info := drain(t, p, c, 1<<20)
	if strings.Join(lines, ",") != "c,d,e" {
		t.Fatalf("lines = %v", lines)
	}
	if info.Lost {
		t.Error("rotated file was found, nothing is lost")
	}
}

func TestReadLogRotatedFileGone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sites-access.log")
	appendFile(t, p, "a\n")
	_, c, _ := drain(t, p, Cursor{}, 1<<20)
	appendFile(t, p, "lost\n")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p, "fresh\n")
	lines, _, info := drain(t, p, c, 1<<20)
	if strings.Join(lines, ",") != "fresh" || !info.Lost {
		t.Fatalf("lines = %v lost = %v", lines, info.Lost)
	}
}

func TestReadLogLongLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sites-access.log")
	long := strings.Repeat("x", MaxLineBytes+10)
	appendFile(t, p, "ok-1\n"+long+"\nok-2\n")
	lines, _, info := drain(t, p, Cursor{}, 1<<20)
	if strings.Join(lines, ",") != "ok-1,ok-2" || info.Skipped != 1 {
		t.Fatalf("lines = %v skipped = %d", lines, info.Skipped)
	}
	// A line longer than one read is skipped in fragments without stalling.
	p2 := filepath.Join(t.TempDir(), "sites-access.log")
	appendFile(t, p2, strings.Repeat("y", 3*MaxLineBytes)+"\nok\n")
	lines, _, _ = drain(t, p2, Cursor{}, MaxLineBytes+1)
	if lines[len(lines)-1] != "ok" {
		t.Fatalf("last line = %q", lines[len(lines)-1])
	}
}
