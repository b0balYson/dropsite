package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// dirBomb builds a tar.gz with n empty top-level directories and index.html.
func dirBomb(t *testing.T, n int) string {
	t.Helper()
	ents := make([]tent, 0, n+1)
	for i := range n {
		ents = append(ents, tent{name: fmt.Sprintf("d%07d/", i), typ: tar.TypeDir})
	}
	ents = append(ents, tent{name: "index.html", body: page})
	return makeTarGz(t, ents...)
}

func TestExtractCountsDirectories(t *testing.T) {
	lim := Limits{MaxTotalBytes: 50 << 20, MaxFileBytes: 20 << 20, MaxFiles: 2000}
	cases := []struct {
		name string
		src  func(t *testing.T) string
	}{
		{"explicit empty dirs", func(t *testing.T) string { return dirBomb(t, 10000) }},
		{"implicit parents", func(t *testing.T) string {
			ents := []tent{{name: "index.html", body: page}}
			for i := range 2001 {
				ents = append(ents, tent{name: fmt.Sprintf("p%05d/x/", i), typ: tar.TypeDir})
			}
			return makeTarGz(t, ents...)
		}},
		{"zip implicit parents", func(t *testing.T) string {
			ents := []zent{{name: "index.html", body: page}}
			for i := range 1100 {
				ents = append(ents, zent{name: fmt.Sprintf("p%05d/q/", i)})
			}
			return makeZip(t, ents...)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, dest := sandbox(t)
			_, err := Extract(context.Background(), tc.src(t), dest, lim, testNow)
			de := wantCode(t, err, domain.CodeArchiveTooMany)
			if de.Params["limit"] != lim.MaxFiles || de.Kind != domain.KindTooLarge {
				t.Fatalf("params %v kind %v", de.Params, de.Kind)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination left behind: %v", err)
			}
			if got := listTree(t, base); len(got) != 2 {
				t.Fatalf("unexpected leftovers: %v", got)
			}
		})
	}
}

func TestExtractDirectoriesWithinLimit(t *testing.T) {
	lim := Limits{MaxFiles: 10}
	ents := []tent{{name: "index.html", body: page}}
	for i := range 10 {
		ents = append(ents, tent{name: fmt.Sprintf("d%d/", i), typ: tar.TypeDir})
	}
	_, dest := sandbox(t)
	if _, err := Extract(context.Background(), makeTarGz(t, ents...), dest, lim, testNow); err != nil {
		t.Fatalf("10 directories with MaxFiles=10 must pass: %v", err)
	}
}

func deepName(depth int) string {
	segs := make([]string, depth)
	for i := range segs {
		segs[i] = "d"
	}
	return strings.Join(segs, "/")
}

func TestExtractShapeLimits(t *testing.T) {
	longName := strings.Repeat(strings.Repeat("a", 200)+"/", 5) + strings.Repeat("b", 30) // 1030 bytes
	cases := []struct {
		name  string
		entry string
		ok    bool
	}{
		{"depth at limit", deepName(MaxDepth-1) + "/f.txt", true},
		{"depth over limit", deepName(MaxDepth) + "/f.txt", false},
		{"dir depth over limit", deepName(MaxDepth+1) + "/", false},
		{"length at limit", strings.Repeat("a", 200) + "/" + strings.Repeat("b", 200) + "/" + strings.Repeat("c", 200) + "/" +
			strings.Repeat("d", 200) + "/" + strings.Repeat("e", MaxPathLen-4*201), true},
		{"length over limit", longName, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dest := sandbox(t)
			body := "x"
			if strings.HasSuffix(tc.entry, "/") {
				body = ""
			}
			ents := []zent{{name: "index.html", body: page}, {name: tc.entry, body: body}}
			_, err := Extract(context.Background(), makeZip(t, ents...), dest, bigLimits, testNow)
			if tc.ok {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				return
			}
			de := wantCode(t, err, domain.CodeArchiveBadName)
			if de.Params["path"] != tc.entry {
				t.Fatalf("params %v", de.Params)
			}
			if de.Params["max_depth"] != MaxDepth || de.Params["max_path_len"] != MaxPathLen {
				t.Fatalf("limits missing from params: %v", de.Params)
			}
		})
	}
}

func TestExtractShapeIgnoresSkippedEntries(t *testing.T) {
	_, dest := sandbox(t)
	src := makeZip(t, zent{name: "index.html", body: page}, zent{name: "node_modules/" + deepName(MaxDepth+5) + "/x.js", body: "x"})
	if _, err := Extract(context.Background(), src, dest, bigLimits, testNow); err != nil {
		t.Fatalf("ignored entries must not hit shape limits: %v", err)
	}
}

func TestWriteTarGzCountsDirectories(t *testing.T) {
	dir := mkTree(t, map[string]string{"index.html": page})
	for i := range 4 {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("e%d", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	_, err := WriteTarGz(context.Background(), dir, &buf, Limits{MaxFiles: 3})
	de := wantCode(t, err, domain.CodeArchiveTooMany)
	if de.Params["limit"] != 3 || buf.Len() != 0 {
		t.Fatalf("params %v, written %d", de.Params, buf.Len())
	}
}

func TestWriteTarGzShapeLimits(t *testing.T) {
	dir := mkTree(t, map[string]string{"index.html": page, deepName(MaxDepth) + "/f.txt": "x"})
	var buf bytes.Buffer
	_, err := WriteTarGz(context.Background(), dir, &buf, bigLimits)
	de := wantCode(t, err, domain.CodeArchiveBadName)
	if de.Params["max_depth"] != MaxDepth || buf.Len() != 0 {
		t.Fatalf("params %v, written %d", de.Params, buf.Len())
	}
}
