// Package archtest enforces package boundaries that keep access control in a
// single layer: adapters (api, ui) must go through internal/service.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/b0balYson/dropsite/internal/"

// forbidden maps an adapter package to imports it must not use. Importing
// store for plain types (filters, pages) is allowed only via service aliases,
// so the whole package is off limits.
var forbidden = map[string][]string{
	"api": {"store", "storage", "archive"},
	"ui":  {"store", "storage", "archive"},
	// MCP and OAuth adapters; internal/mcp is also linked into the CLI, so
	// it must not reach the server at all.
	"oauth":      {"store", "storage", "archive"},
	"mcp/remote": {"store", "storage", "archive"},
	"mcp":        {"store", "storage", "archive", "config"},
}

func TestAdaptersDoNotBypassService(t *testing.T) {
	for pkg, banned := range forbidden {
		dir := filepath.Join("..", pkg)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				for _, b := range banned {
					if p == module+b || strings.HasPrefix(p, module+b+"/") {
						t.Errorf("%s imports %s: adapters must use internal/service", path, p)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
