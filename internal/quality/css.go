package quality

import (
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	cssURLRe    = regexp.MustCompile(`url\(\s*(?:"([^"]*)"|'([^']*)'|([^)"'\s]*))\s*\)`)
	cssImportRe = regexp.MustCompile(`@import\s+(?:"([^"]*)"|'([^']*)')`)
)

func firstGroup(m []string) string {
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// cssRefs checks url() and @import references of CSS text found in base
// (a stylesheet, or a page for <style> blocks and style attributes).
func (a *analyzer) cssRefs(text, base string, assets map[string]bool) {
	for _, m := range cssImportRe.FindAllStringSubmatch(text, -1) {
		a.ref(base, "stylesheet", firstGroup(m), assets)
	}
	for _, m := range cssURLRe.FindAllStringSubmatch(text, -1) {
		u := firstGroup(m)
		if u == "" {
			continue
		}
		a.ref(base, "", u, assets)
	}
}

func isCSS(p string) bool { return strings.ToLower(path.Ext(p)) == ".css" }

// cssFile analyzes a stylesheet once and returns the local files it loads.
func (a *analyzer) cssFile(p string) []string {
	if deps, ok := a.cssDeps[p]; ok || a.cssDone[p] {
		return deps
	}
	a.cssDone[p] = true
	if a.cssCount >= MaxCSSFiles {
		a.rep.Truncated = true
		return nil
	}
	a.cssCount++
	rc, cut, err := a.open(p, MaxCSSBytes)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	if cut {
		a.rep.Truncated = true
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	a.cssRefs(string(data), p, set)
	deps := make([]string, 0, len(set))
	for d := range set {
		deps = append(deps, d)
	}
	sort.Strings(deps)
	a.cssDeps[p] = deps
	return deps
}

// cssFiles checks the stylesheets no page loaded.
func (a *analyzer) cssFiles() {
	var all []string
	for p := range a.files {
		if isCSS(p) && !a.cssDone[p] {
			all = append(all, p)
		}
	}
	sort.Strings(all)
	for _, p := range all {
		if a.ctx.Err() != nil {
			return
		}
		a.cssFile(p)
	}
}

// weight is the page plus the local files it loads, including what its
// stylesheets load (one level), each file counted once.
func (a *analyzer) weight(st *pageState) int64 {
	total := a.files[st.path]
	counted := map[string]bool{st.path: true}
	add := func(f string) {
		if !counted[f] {
			counted[f] = true
			total += a.files[f]
		}
	}
	names := make([]string, 0, len(st.assets))
	for f := range st.assets {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		add(f)
		if isCSS(f) {
			for _, d := range a.cssFile(f) {
				add(d)
			}
		}
	}
	return total
}
