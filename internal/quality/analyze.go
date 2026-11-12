package quality

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/b0balYson/dropsite/internal/brand"
)

// Options describe the site whose release is analyzed.
type Options struct {
	// Hosts are the site's own host names: absolute links to them are
	// checked as internal links.
	Hosts []string
	// Generated lists root files the platform serves although the release
	// has none of its own (robots.txt, sitemap.xml, favicon.ico).
	Generated []string
}

// analyzer holds the state of one run.
type analyzer struct {
	ctx   context.Context
	root  *os.Root
	files map[string]int64
	hosts map[string]bool
	spa   bool
	rep   Report

	seen     map[string]bool // de-duplicates issues
	cssDeps  map[string][]string
	cssDone  map[string]bool
	cssCount int
	weights  []PageSize
}

// Analyze checks the release in dir. Files under the platform's generated
// directory are ignored.
func Analyze(ctx context.Context, dir string, o Options) (Report, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Report{}, fmt.Errorf("open release: %w", err)
	}
	defer func() { _ = root.Close() }()
	a := &analyzer{
		ctx: ctx, root: root, files: map[string]int64{}, hosts: map[string]bool{},
		seen: map[string]bool{}, cssDeps: map[string][]string{}, cssDone: map[string]bool{},
	}
	for _, h := range o.Hosts {
		a.hosts[strings.ToLower(h)] = true
	}
	pages, err := a.scan()
	if err != nil {
		return Report{}, err
	}
	for _, g := range o.Generated {
		if _, ok := a.files[g]; !ok {
			a.files[g] = 0
		}
	}
	_, a.spa = a.files["200.html"]
	if len(pages) > MaxPages {
		pages = pages[:MaxPages]
		a.rep.Truncated = true
	}
	for _, p := range pages {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		if err := a.page(p); err != nil {
			return Report{}, err
		}
	}
	a.cssFiles()
	a.sizes()
	a.rep.finish()
	return a.rep, nil
}

// scan lists the site's files with sizes and returns its pages in order.
func (a *analyzer) scan() ([]string, error) {
	var pages []string
	err := fs.WalkDir(a.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == brand.GeneratedDir {
			return fs.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		a.files[p] = info.Size()
		a.rep.Files++
		a.rep.TotalBytes += info.Size()
		if isPage(p) {
			pages = append(pages, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk release: %w", err)
	}
	a.rep.Pages = len(pages)
	return pages, nil
}

func isPage(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".html" || ext == ".htm"
}

// open reads at most limit bytes of a file and reports whether it was cut.
func (a *analyzer) open(p string, limit int64) (io.ReadCloser, bool, error) {
	f, err := a.root.Open(p)
	if err != nil {
		return nil, false, err
	}
	cut := a.files[p] > limit
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(f, limit), f}, cut, nil
}

// issue records a de-duplicated problem in a capped list and returns
// whether it was new.
func (a *analyzer) issue(list *[]Issue, count *int, i Issue, category string) {
	key := category + "\x00" + i.Page + "\x00" + i.Target
	if a.seen[key] {
		return
	}
	a.seen[key] = true
	*count++
	if len(*list) < MaxListItems {
		*list = append(*list, i)
	}
}

func (a *analyzer) pageMissing(list *[]string, count *int, page string) {
	*count++
	if len(*list) < MaxListItems {
		*list = append(*list, page)
	}
}

func (a *analyzer) external(page, kind, target, host string) {
	a.issue(&a.rep.ExternalResources, &a.rep.Counts.ExternalResources,
		Issue{Page: page, Target: target, Kind: kind}, "external")
	for _, h := range a.rep.ExternalHosts {
		if h == host {
			return
		}
	}
	a.rep.ExternalHosts = append(a.rep.ExternalHosts, host)
}

// sizes fills the heaviest pages and the largest files.
func (a *analyzer) sizes() {
	sort.SliceStable(a.weights, func(i, j int) bool { return a.weights[i].Bytes > a.weights[j].Bytes })
	for _, w := range a.weights {
		if w.Bytes > HeavyPageBytes {
			a.rep.Counts.HeavyPages++
		}
	}
	a.rep.HeaviestPages = a.weights[:min(topN, len(a.weights))]
	files := make([]FileSize, 0, len(a.files))
	for p, n := range a.files {
		if n > 0 {
			files = append(files, FileSize{Path: p, Bytes: n})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Bytes != files[j].Bytes {
			return files[i].Bytes > files[j].Bytes
		}
		return files[i].Path < files[j].Path
	})
	a.rep.LargestFiles = files[:min(topN, len(files))]
	sort.Strings(a.rep.ExternalHosts)
}
