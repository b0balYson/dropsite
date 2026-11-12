package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
)

// pageTemplate is the layout plus partials plus one page's blocks.
type pageTemplate = template.Template

// fragmentSet names the template set that renders partials on their own.
const fragmentSet = "_fragments"

var funcs = template.FuncMap{
	"host": displayHost,
	"wbr":  breakable,
	"inc":  func(i int) int { return i + 1 },
	"join": strings.Join,
	"dict": dict,
}

// dict builds a map from key/value pairs so a template can pass several
// values to another template.
func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	return m
}

// breakable escapes s and adds line-break opportunities after dots, "@" and
// "/" and before ":", so long host names and emails wrap between labels
// instead of in the middle of a word.
func breakable(s string) template.HTML {
	var b strings.Builder
	for _, r := range s {
		if r == ':' {
			b.WriteString("<wbr>")
		}
		b.WriteString(template.HTMLEscapeString(string(r)))
		if r == '.' || r == '@' || r == '/' {
			b.WriteString("<wbr>")
		}
	}
	return template.HTML(b.String()) //nolint:gosec // every rune is escaped above; only <wbr> is added
}

// displayHost strips the scheme and trailing slash from a site URL.
func displayHost(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimSuffix(u, "/")
}

func parsePages() (map[string]*pageTemplate, error) {
	base, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	files, err := fs.Glob(assets, "templates/pages/*.html")
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}
	out := map[string]*pageTemplate{fragmentSet: base}
	for _, f := range files {
		t, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone layout: %w", err)
		}
		if _, err := t.ParseFS(assets, f); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		out[strings.TrimSuffix(path.Base(f), ".html")] = t
	}
	return out, nil
}

// view is the data every template receives.
type view struct {
	L       *locale
	Me      *domain.User
	IsAdmin bool
	CSRF    string
	Nav     string
	Title   string
	Flash   *flash
	Path    string
	Product string
	assets  map[string]string
	D       any
}

// Asset returns the cache-busting URL of a static file.
func (v view) Asset(name string) string { return v.assets[name] }

func (s *server) newView(r *http.Request, rc *reqCtx, nav, title string, data any) view {
	v := view{
		L: rc.loc, CSRF: rc.csrf, Nav: nav, Title: title, Path: r.URL.RequestURI(),
		Product: brand.DisplayName, assets: s.assetURL, D: data,
	}
	if rc.authed {
		u := rc.actor.User
		v.Me, v.IsAdmin = &u, u.IsAdmin()
	}
	return v
}

// page renders a full page with the layout.
func (s *server) page(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, name, nav, title string, data any) {
	v := s.newView(r, rc, nav, title, data)
	v.Flash = s.takeFlash(w, r)
	s.execute(w, status, s.pages[name], "layout.html", v)
}

// fragment renders a partial template for htmx requests.
func (s *server) fragment(w http.ResponseWriter, r *http.Request, rc *reqCtx, name string, data any) {
	s.execute(w, http.StatusOK, s.pages[fragmentSet], name, s.newView(r, rc, "", "", data))
}

func (s *server) execute(w http.ResponseWriter, status int, t *pageTemplate, name string, v view) {
	var buf bytes.Buffer
	if t == nil {
		s.log.Error("template set missing", "template", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(&buf, name, v); err != nil {
		s.log.Error("render template", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.page(w, r, rc, http.StatusNotFound, "error", "", "error.notfound.title",
		errorPage{Code: 404, Heading: "error.notfound.title", Text: "error.notfound.text"})
}

func (s *server) forbidden(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusForbidden, deployReply{Message: rc.loc.T("error.forbidden.text")})
		return
	}
	s.page(w, r, rc, http.StatusForbidden, "error", "", "error.forbidden.title",
		errorPage{Code: 403, Heading: "error.forbidden.title", Text: "error.forbidden.text"})
}

type errorPage struct {
	Code    int
	Heading string
	Text    string
	Message string
}

// redirect sends the browser to target after a POST (post/redirect/get).
// Targets are fixed paths or user input that passed safeNext.
func redirect(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: callers pass only same-site paths (see safeNext)
}
