// Package optimize adds generated files to a release directory at publish
// time: WebP variants of JPEG and PNG images and fallback robots.txt,
// sitemap.xml and favicon.ico. It never changes, removes or renames a file of
// the site: everything it writes lives under Dir, which Caddy only serves in
// place of a missing file or, for images, to browsers that accept WebP.
//
// Run never fails: a problem with one step or one file is logged and
// skipped, so a publication never fails because of an optimization.
package optimize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
)

// Layout of the generated directory inside a release.
const (
	// Dir is the release-root directory holding generated files. Archives
	// may not contain it (archive.IsIgnored) and Caddy answers 404 to direct
	// requests (hidden path).
	Dir = brand.GeneratedDir
	// WebPDir holds <path>.webp for every converted image <path>.
	WebPDir = Dir + "/webp"
	// StaticDir holds files served only when the site has none at that path.
	StaticDir = Dir + "/static"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// Options describe the site being published.
type Options struct {
	// SiteURL is the primary address (https://host, no trailing slash) used
	// for absolute sitemap addresses.
	SiteURL string
	// SiteID seeds the colour of the generated favicon.
	SiteID string
	// Noindex skips robots.txt and sitemap.xml.
	Noindex bool
	// Deadline stops image conversion when reached; zero means no deadline.
	Deadline time.Time
	Logger   *slog.Logger
}

// ImageStats summarize image conversion.
type ImageStats struct {
	Candidates  int   `json:"candidates"`
	Converted   int   `json:"converted"`
	BytesBefore int64 `json:"bytes_before"`
	BytesAfter  int64 `json:"bytes_after"`
}

// Result reports what Run did.
type Result struct {
	Images ImageStats `json:"images"`
	// Generated lists the fallback files created (robots.txt, ...).
	Generated []string `json:"generated"`
	// TimedOut is true when images were left because of the deadline.
	TimedOut bool `json:"timed_out"`
	// Failures counts files or steps skipped because of an error.
	Failures   int   `json:"failures"`
	DurationMS int64 `json:"duration_ms"`
}

// IsGenerated reports whether a slash-separated release path lies in Dir.
func IsGenerated(rel string) bool {
	rel = strings.TrimPrefix(rel, "/")
	return rel == Dir || strings.HasPrefix(rel, Dir+"/")
}

// tree is what Run needs to know about the site's own files.
type tree struct {
	images []imageFile
	pages  []string
	has    map[string]bool // root-level fallback names present
}

type imageFile struct {
	path string
	size int64
}

// Run optimizes the release in dir. It is safe to call on any extracted
// release; it only writes below Dir.
func Run(ctx context.Context, dir string, o Options) Result {
	start := time.Now()
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	r := &runner{ctx: ctx, o: o, log: log}
	root, err := os.OpenRoot(dir)
	if err != nil {
		log.Warn("optimize: open release", "err", err)
		return Result{Failures: 1}
	}
	defer func() { _ = root.Close() }()
	r.root = root
	r.run()
	r.res.DurationMS = time.Since(start).Milliseconds()
	return r.res
}

type runner struct {
	ctx  context.Context
	o    Options
	log  *slog.Logger
	root *os.Root
	res  Result
}

func (r *runner) run() {
	if err := r.root.RemoveAll(Dir); err != nil {
		r.fail("clear generated directory", Dir, err)
		return
	}
	t, err := scan(r.root)
	if err != nil {
		r.fail("scan release", ".", err)
		return
	}
	r.static(t)
	r.images(t.images)
}

func (r *runner) fail(step, p string, err error) {
	r.res.Failures++
	r.log.Warn("optimize: step skipped", "step", step, "path", p, "err", err)
}

// scan lists the site's images, pages and root fallback files.
func scan(root *os.Root) (tree, error) {
	t := tree{has: map[string]bool{}}
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if IsGenerated(p) {
			return fs.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if !strings.Contains(p, "/") {
			t.has[p] = true
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".jpg", ".jpeg", ".png":
			info, err := d.Info()
			if err != nil {
				return err
			}
			t.images = append(t.images, imageFile{path: p, size: info.Size()})
		case ".html", ".htm":
			t.pages = append(t.pages, p)
		}
		return nil
	})
	if err != nil {
		return tree{}, fmt.Errorf("walk release: %w", err)
	}
	return t, nil
}

// write stores data at a generated path, creating directories.
func (r *runner) write(rel string, data []byte) error {
	if !IsGenerated(rel) {
		return errors.New("refusing to write outside the generated directory")
	}
	if err := r.root.MkdirAll(path.Dir(rel), dirPerm); err != nil {
		return err
	}
	return r.root.WriteFile(rel, data, filePerm)
}

func (r *runner) expired() bool {
	if r.ctx.Err() != nil {
		return true
	}
	return !r.o.Deadline.IsZero() && !time.Now().Before(r.o.Deadline)
}
