package archive

import (
	"testing"

	"github.com/b0balYson/dropsite/internal/brand"
)

func TestIsIgnored(t *testing.T) {
	cases := map[string]bool{
		".git":                     true,
		".dropsite":                true,
		".dropsite/webp/a.png.webp": true,
		"sub/.dropsite/x":          false,
		".dropsitex/a":             false,
		".git/config":              true,
		"a/.git/HEAD":              true,
		".DS_Store":                true,
		"img/.DS_Store":            true,
		"__MACOSX/._a":             true,
		"Thumbs.db":                true,
		"a/b/Thumbs.db":            true,
		"node_modules/x/index.js":  true,
		"web/node_modules/":        true,
		brand.ProjectFile:          true,
		"/" + brand.ProjectFile:    true,
		"sub/" + brand.ProjectFile: false,
		"index.html":               false,
		".gitignore":               false,
		"git/x":                    false,
		"my.DS_Store":              false,
		"node_modules.js":          false,
		".well-known/x":            false,
	}
	for rel, want := range cases {
		if got := IsIgnored(rel); got != want {
			t.Errorf("IsIgnored(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestCleanName(t *testing.T) {
	ok := map[string]string{
		"index.html":      "index.html",
		"./index.html":    "index.html",
		"a/./b":           "a/b",
		"dist/":           "dist",
		"./":              "",
		"a b/ф.png":       "a b/ф.png",
		"...":             "...",
		"..a/b..":         "..a/b..",
		"ab:c":            "ab:c",
		".well-known/x.y": ".well-known/x.y",
	}
	for raw, want := range ok {
		got, err := cleanName(raw)
		if err != nil || got != want {
			t.Errorf("cleanName(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}
