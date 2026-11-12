package archive

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const indexFile = "index.html"

// hoist makes the contents of a single top-level directory the root. Ignored
// entries were never written, so the on-disk root already excludes them.
func hoist(root *os.Root) error {
	top, err := readDir(root, ".")
	if err != nil {
		return err
	}
	if len(top) != 1 || !top[0].IsDir() {
		return nil
	}
	wrapper := top[0].Name()
	children, err := readDir(root, wrapper)
	if err != nil {
		return err
	}
	tmp := freeName(children)
	if err := root.Rename(wrapper, tmp); err != nil {
		return internalErr("move wrapper directory", err)
	}
	for _, c := range children {
		if err := root.Rename(filepath.Join(tmp, c.Name()), c.Name()); err != nil {
			return internalErr("lift wrapper directory entry", err)
		}
	}
	if err := root.Remove(tmp); err != nil {
		return internalErr("remove wrapper directory", err)
	}
	return nil
}

func readDir(root *os.Root, name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(root.FS(), name)
	if err != nil {
		return nil, internalErr("read directory", err)
	}
	return entries, nil
}

// freeName picks a temporary name that does not clash with any child.
func freeName(children []fs.DirEntry) string {
	taken := make(map[string]bool, len(children))
	for _, c := range children {
		taken[c.Name()] = true
	}
	name := ".hoist"
	for i := 0; taken[name]; i++ {
		name = ".hoist" + strconv.Itoa(i)
	}
	return name
}

func requireIndex(root *os.Root) error {
	fi, err := root.Lstat(indexFile)
	if err == nil && fi.Mode().IsRegular() {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return internalErr("stat index.html", err)
	}
	return fail(domain.CodeArchiveNoIndex, "index.html is missing at the root", nil)
}

// normalize sets 0755/0644 regardless of umask and the archive, and stamps
// every entry with now: Caddy derives ETags from mtime and size.
func normalize(root *os.Root, now time.Time) error {
	return fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return internalErr("walk extracted tree", err)
		}
		perm := filePerm
		switch {
		case d.IsDir():
			perm = dirPerm
		case !d.Type().IsRegular():
			return internalErr("unexpected file type after extraction", errors.New(p))
		}
		name := filepath.FromSlash(p)
		if err := root.Chmod(name, perm); err != nil {
			return internalErr("set permissions", err)
		}
		if err := root.Chtimes(name, now, now); err != nil {
			return internalErr("set times", err)
		}
		return nil
	})
}
