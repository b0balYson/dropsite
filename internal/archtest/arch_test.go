// Package archtest enforces package boundaries that keep access control in a
// single layer: adapters (api, ui, siteauth) must go through internal/service.
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
	"api":      {"store", "storage", "archive", "updates"},
	"ui":       {"store", "storage", "archive"},
	"siteauth": {"store", "storage", "archive", "updates"},
	// MCP and OAuth adapters; internal/mcp is also linked into the CLI, so
	// it must not reach the server at all.
	"oauth":      {"store", "storage", "archive", "updates"},
	"mcp/remote": {"store", "storage", "archive", "updates"},
	"mcp":        {"store", "storage", "archive", "config", "updates"},
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

// updateRequesters are the only files that may ask the server helper to
// update: the settings page of an administrator signed in to the web
// interface. API, OAuth and MCP paths must never reach it, so neither a
// token nor prompt-injected content can update the server.
var updateRequesters = map[string]bool{
	filepath.Join("..", "ui", "pages_admin_updates.go"): true,
	filepath.Join("..", "service", "updates.go"):        true,
	filepath.Join("..", "updates", "files.go"):          true,
}

func TestOnlyTheWebInterfaceRequestsUpdates(t *testing.T) {
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path) //nolint:gosec // source files of this repository
			if err != nil {
				return err
			}
			src := string(b)
			if (strings.Contains(src, ".RequestUpdate(") || strings.Contains(src, ".WriteRequest(")) && !updateRequesters[path] {
				t.Errorf("%s requests an update: only the administrator's web interface may", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
