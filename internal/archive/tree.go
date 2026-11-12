package archive

import "github.com/b0balYson/dropsite/internal/domain"

type entryType int

const (
	typeFile entryType = iota
	typeDir
	typeLink
	typeSpecial
)

type nodeKind uint8

const (
	nodeImplicitDir nodeKind = iota + 1
	nodeDir
	nodeFile
)

// tree is the in-memory namespace of accepted entries. It rejects duplicates
// and file/directory conflicts and counts stored files against MaxFiles.
type tree struct {
	lim   Limits
	nodes map[string]nodeKind
	files int
}

func newTree(lim Limits) *tree {
	return &tree{lim: lim, nodes: make(map[string]nodeKind)}
}

// admit validates one archive entry. It returns the normalized path and
// whether the entry is skipped (archive root, ignored service files).
// Names are checked first, so even ignored entries cannot carry unsafe names;
// types are checked only for entries that would be stored.
func (t *tree) admit(raw string, typ entryType) (rel string, skip bool, err error) {
	rel, err = cleanName(raw)
	if err != nil {
		return "", false, err
	}
	if rel == "" {
		if typ == typeDir {
			return "", true, nil
		}
		return "", false, pathErr(domain.CodeArchiveBadName, "entry has no name", raw)
	}
	if IsIgnored(rel) {
		return "", true, nil
	}
	switch typ {
	case typeLink:
		return "", false, pathErr(domain.CodeArchiveLink, "archive contains a symbolic or hard link", raw)
	case typeSpecial:
		return "", false, pathErr(domain.CodeArchiveSpecialFile, "archive contains a device, FIFO or other special file", raw)
	}
	return rel, false, t.add(rel, raw, typ == typeDir)
}

func (t *tree) add(rel, raw string, isDir bool) error {
	if err := t.addParents(rel, raw); err != nil {
		return err
	}
	prev, exists := t.nodes[rel]
	if isDir {
		if exists && prev != nodeImplicitDir {
			return conflictErr(raw)
		}
		t.nodes[rel] = nodeDir
		return nil
	}
	if exists {
		return conflictErr(raw)
	}
	if t.files >= t.lim.filesCap() {
		return tooManyErr(t.lim)
	}
	t.files++
	t.nodes[rel] = nodeFile
	return nil
}

func (t *tree) addParents(rel, raw string) error {
	for i := 0; i < len(rel); i++ {
		if rel[i] != '/' {
			continue
		}
		parent := rel[:i]
		switch t.nodes[parent] {
		case nodeFile:
			return conflictErr(raw)
		case 0:
			t.nodes[parent] = nodeImplicitDir
		}
	}
	return nil
}

func conflictErr(raw string) *domain.Error {
	return pathErr(domain.CodeArchiveBadName, "duplicate entry or file/directory conflict", raw)
}
