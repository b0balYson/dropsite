package archive

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

const (
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
)

// writer materializes accepted entries inside an os.Root, which also
// guarantees at the kernel level that nothing escapes the destination.
type writer struct {
	root   *os.Root
	dirs   map[string]bool
	budget *budget
}

func newWriter(root *os.Root, lim Limits) *writer {
	return &writer{root: root, dirs: make(map[string]bool), budget: newBudget(lim)}
}

// mkdirAll creates rel and its missing parents. The tree has already ruled
// out conflicts, so any failure here is unexpected.
func (w *writer) mkdirAll(rel string) error {
	for i := 0; i <= len(rel); i++ {
		if i < len(rel) && rel[i] != '/' {
			continue
		}
		p := rel[:i]
		if p == "" || w.dirs[p] {
			continue
		}
		if err := w.root.Mkdir(filepath.FromSlash(p), dirPerm); err != nil {
			return internalErr("create directory", err)
		}
		w.dirs[p] = true
	}
	return nil
}

func (w *writer) writeFile(ctx context.Context, rel, raw string, src io.Reader) error {
	if dir := parentOf(rel); dir != "" {
		if err := w.mkdirAll(dir); err != nil {
			return err
		}
	}
	f, err := w.root.OpenFile(filepath.FromSlash(rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return internalErr("create file", err)
	}
	cerr := w.budget.copy(ctx, f, src, raw)
	if err := f.Close(); err != nil && cerr == nil {
		return internalErr("close file", err)
	}
	return cerr
}

func parentOf(rel string) string {
	for i := len(rel) - 1; i >= 0; i-- {
		if rel[i] == '/' {
			return rel[:i]
		}
	}
	return ""
}
