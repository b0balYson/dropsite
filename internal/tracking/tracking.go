// Package tracking injects third-party analytics counters (Yandex Metrica,
// Google Analytics 4) into the HTML files of a release. Only counter IDs of a
// strict format are accepted and the snippet is built from constant
// templates, so a setting can never inject arbitrary markup.
package tracking

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

var (
	metrikaRe = regexp.MustCompile(`^[1-9][0-9]{3,11}$`)
	ga4Re     = regexp.MustCompile(`^G-[A-Z0-9]{4,16}$`)
)

// Markers delimit the injected block so it can be replaced or removed.
const (
	beginMarker = "<!-- dropsite:counters -->"
	endMarker   = "<!-- /dropsite:counters -->"
)

// Field names reported in counter_id_invalid errors.
const (
	FieldMetrika = "metrika_id"
	FieldGA4     = "ga4_id"
)

func invalid(field string) error {
	return domain.E(domain.KindInvalid, domain.CodeCounterInvalid, field+" is not a valid counter ID").
		WithParams(map[string]any{"field": field})
}

// Normalize trims and validates counter IDs: the Metrica ID is 4-12 digits
// without a leading zero, the GA4 ID is G- plus 4-16 letters or digits
// (upper-cased). Empty values are allowed and mean "no counter".
func Normalize(c domain.Counters) (domain.Counters, error) {
	out := domain.Counters{
		MetrikaID: strings.TrimSpace(c.MetrikaID),
		GA4ID:     strings.ToUpper(strings.TrimSpace(c.GA4ID)),
	}
	if out.MetrikaID != "" && !metrikaRe.MatchString(out.MetrikaID) {
		return domain.Counters{}, invalid(FieldMetrika)
	}
	if out.GA4ID != "" && !ga4Re.MatchString(out.GA4ID) {
		return domain.Counters{}, invalid(FieldGA4)
	}
	return out, nil
}

// Set is the list of counters effective for a site.
type Set struct {
	Metrika []string
	GA4     []string
}

// Merge combines counter settings (site and global), dropping empty and
// repeated IDs. Every ID must already be normalized.
func Merge(cs ...domain.Counters) Set {
	var s Set
	for _, c := range cs {
		if c.MetrikaID != "" && !slices.Contains(s.Metrika, c.MetrikaID) {
			s.Metrika = append(s.Metrika, c.MetrikaID)
		}
		if c.GA4ID != "" && !slices.Contains(s.GA4, c.GA4ID) {
			s.GA4 = append(s.GA4, c.GA4ID)
		}
	}
	return s
}

// Empty reports whether no counter is set.
func (s Set) Empty() bool { return len(s.Metrika) == 0 && len(s.GA4) == 0 }

// valid re-checks every ID: Snippet must never emit anything else.
func (s Set) valid() bool {
	for _, id := range s.Metrika {
		if !metrikaRe.MatchString(id) {
			return false
		}
	}
	for _, id := range s.GA4 {
		if !ga4Re.MatchString(id) {
			return false
		}
	}
	return true
}

const metrikaLoader = `<script>(function(m,e,t,r,i,k,a){m[i]=m[i]||function(){(m[i].a=m[i].a||[]).push(arguments)};` +
	`m[i].l=1*new Date();for(var j=0;j<document.scripts.length;j++){if(document.scripts[j].src===r){return;}}` +
	`k=e.createElement(t),a=e.getElementsByTagName(t)[0],k.async=1,k.src=r,a.parentNode.insertBefore(k,a)})` +
	`(window,document,"script","https://mc.yandex.ru/metrika/tag.js","ym");`

// Snippet returns the marked block with the standard code of every counter,
// or nil for an empty set.
func (s Set) Snippet() ([]byte, error) {
	if s.Empty() {
		return nil, nil
	}
	if !s.valid() {
		return nil, errors.New("tracking: invalid counter ID in set")
	}
	var b bytes.Buffer
	b.WriteString(beginMarker + "\n")
	if len(s.Metrika) > 0 {
		b.WriteString(metrikaLoader)
		for _, id := range s.Metrika {
			fmt.Fprintf(&b, `ym(%s,"init",{clickmap:true,trackLinks:true,accurateTrackBounce:true});`, id)
		}
		b.WriteString("</script>\n")
	}
	if len(s.GA4) > 0 {
		fmt.Fprintf(&b, `<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>`+"\n", s.GA4[0])
		b.WriteString(`<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag("js",new Date());`)
		for _, id := range s.GA4 {
			fmt.Fprintf(&b, `gtag("config","%s");`, id)
		}
		b.WriteString("</script>\n")
	}
	b.WriteString(endMarker + "\n")
	return b.Bytes(), nil
}

// Apply returns content with any previous Dropsite block removed and snippet
// (which may be nil) inserted before the first </head>, else before the first
// <body, else at the end. It never inserts at the start: a script before
// <!doctype> would switch the page to quirks mode.
func Apply(content, snippet []byte) []byte {
	out := strip(content)
	if len(snippet) == 0 {
		return out
	}
	at := indexTag(out, "</head")
	if at < 0 {
		at = indexTag(out, "<body")
	}
	if at < 0 {
		at = len(out)
	}
	res := make([]byte, 0, len(out)+len(snippet))
	res = append(res, out[:at]...)
	res = append(res, snippet...)
	return append(res, out[at:]...)
}

// strip removes every complete Dropsite block.
func strip(content []byte) []byte {
	out := content
	for {
		i := bytes.Index(out, []byte(beginMarker))
		if i < 0 {
			return out
		}
		j := bytes.Index(out[i:], []byte(endMarker))
		if j < 0 {
			return out
		}
		end := i + j + len(endMarker)
		if end < len(out) && out[end] == '\n' {
			end++
		}
		out = append(out[:i:i], out[end:]...)
	}
}

// indexTag finds the first tag starting with tag (ASCII, any case) that is
// followed by '>', '/' or white space, so "</head" does not match "</header>".
func indexTag(s []byte, tag string) int {
	n := len(tag)
	for i := 0; i+n < len(s); i++ {
		if !bytes.EqualFold(s[i:i+n], []byte(tag)) {
			continue
		}
		switch s[i+n] {
		case '>', '/', ' ', '\t', '\n', '\r', '\f':
			return i
		}
	}
	return -1
}

// IsHTML reports whether a release file is processed: .html or .htm.
func IsHTML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".html" || ext == ".htm"
}

func isUTF16(b []byte) bool {
	return bytes.HasPrefix(b, []byte{0xFF, 0xFE}) || bytes.HasPrefix(b, []byte{0xFE, 0xFF})
}

// Result reports what Inject changed.
type Result struct {
	Files      int   // HTML files rewritten
	DeltaBytes int64 // size change of the release
}

// Inject applies set to every regular .html/.htm file under dir. Files whose
// content does not change are left untouched (mtime included); rewritten
// files get mode 0644 and mtime now so Caddy's ETag changes. UTF-16 files are
// skipped.
func Inject(dir string, set Set, now time.Time) (Result, error) {
	snippet, err := set.Snippet()
	if err != nil {
		return Result{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Result{}, fmt.Errorf("tracking: open release: %w", err)
	}
	defer root.Close()
	var res Result
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || !IsHTML(p) {
			return nil
		}
		delta, changed, err := rewrite(root, filepath.FromSlash(p), snippet, now)
		if err != nil {
			return err
		}
		if changed {
			res.Files++
			res.DeltaBytes += delta
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("tracking: inject counters: %w", err)
	}
	return res, nil
}

func rewrite(root *os.Root, name string, snippet []byte, now time.Time) (int64, bool, error) {
	content, err := root.ReadFile(name)
	if err != nil {
		return 0, false, err
	}
	if isUTF16(content) {
		return 0, false, nil
	}
	next := Apply(content, snippet)
	if bytes.Equal(next, content) {
		return 0, false, nil
	}
	if err := root.WriteFile(name, next, 0o644); err != nil {
		return 0, false, err
	}
	if err := root.Chmod(name, 0o644); err != nil {
		return 0, false, err
	}
	if err := root.Chtimes(name, now, now); err != nil {
		return 0, false, err
	}
	return int64(len(next) - len(content)), true, nil
}
