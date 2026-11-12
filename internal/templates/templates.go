// Package templates holds the built-in starter sites. Each template is a
// directory under sites/ with meta.json (name and description per
// language), strings.json (page texts per language) and its files; *.html
// files are rendered with html/template, so the site title is always
// escaped. sites/base.css, sites/layout.html and sites/strings.json are
// shared by every template, and every template gets a 404.html.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed sites
var files embed.FS

// Languages lists the template languages; the first is the default.
var Languages = []string{"ru", "en"}

// ErrNotFound is returned for an unknown template name.
var ErrNotFound = errors.New("template not found")

// Info describes a template in one language.
type Info struct {
	Name        string
	Title       string
	Description string
	// SiteTitle is the title used when the user gives none.
	SiteTitle string
}

// File is one rendered file of a site.
type File struct {
	Path string
	Data []byte
}

type langMeta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	SiteTitle   string `json:"site_title"`
}

type meta struct {
	Order int      `json:"order"`
	Theme string   `json:"theme"`
	RU    langMeta `json:"ru"`
	EN    langMeta `json:"en"`
}

func (m meta) lang(l string) langMeta {
	if l == "en" {
		return m.EN
	}
	return m.RU
}

type page struct {
	Title    string
	Lang     string
	Theme    string
	NotFound bool
	T        map[string]string
}

// Lang returns a supported language: l itself, or the default.
func Lang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	for _, v := range Languages {
		if v == l {
			return l
		}
	}
	return Languages[0]
}

func names() ([]string, map[string]meta, error) {
	entries, err := fs.ReadDir(files, "sites")
	if err != nil {
		return nil, nil, err
	}
	metas := map[string]meta{}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := files.ReadFile(path.Join("sites", e.Name(), "meta.json"))
		if err != nil {
			return nil, nil, err
		}
		var m meta
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, nil, fmt.Errorf("template %s: meta.json: %w", e.Name(), err)
		}
		metas[e.Name()] = m
		out = append(out, e.Name())
	}
	sort.SliceStable(out, func(i, j int) bool { return metas[out[i]].Order < metas[out[j]].Order })
	return out, metas, nil
}

// List returns every template in display order, described in lang.
func List(lang string) []Info {
	lang = Lang(lang)
	ns, metas, err := names()
	if err != nil {
		panic("templates: " + err.Error()) // embedded at build time and covered by tests
	}
	out := make([]Info, 0, len(ns))
	for _, n := range ns {
		m := metas[n].lang(lang)
		out = append(out, Info{Name: n, Title: m.Title, Description: m.Description, SiteTitle: m.SiteTitle})
	}
	return out
}

// Get describes one template in lang.
func Get(name, lang string) (Info, error) {
	for _, i := range List(lang) {
		if i.Name == name {
			return i, nil
		}
	}
	return Info{}, ErrNotFound
}

func readStrings(name, lang string) (map[string]string, error) {
	raw, err := files.ReadFile(name)
	if err != nil {
		return nil, err
	}
	all := map[string]map[string]string{}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return all[lang], nil
}

// Render builds the files of template name in lang with the given site
// title (the template's sample title when empty).
func Render(name, lang, title string) ([]File, error) {
	lang = Lang(lang)
	info, err := Get(name, lang)
	if err != nil {
		return nil, err
	}
	_, metas, err := names()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = info.SiteTitle
	}
	t := map[string]string{}
	for _, f := range []string{"sites/strings.json", path.Join("sites", name, "strings.json")} {
		m, err := readStrings(f, lang)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			t[k] = v
		}
	}
	data := page{Title: strings.TrimSpace(title), Lang: lang, Theme: metas[name].Theme, T: t}
	return renderDir(name, data)
}

func renderDir(name string, data page) ([]File, error) {
	layout, err := files.ReadFile("sites/layout.html")
	if err != nil {
		return nil, err
	}
	base, err := files.ReadFile("sites/base.css")
	if err != nil {
		return nil, err
	}
	out := []File{{Path: "base.css", Data: base}}
	dir := path.Join("sites", name)
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == "meta.json" || n == "strings.json" {
			continue
		}
		raw, err := files.ReadFile(path.Join(dir, n))
		if err != nil {
			return nil, err
		}
		if path.Ext(n) == ".html" {
			if raw, err = execute(string(layout), string(raw), "", data); err != nil {
				return nil, fmt.Errorf("template %s: %s: %w", name, n, err)
			}
		}
		out = append(out, File{Path: n, Data: raw})
	}
	nf := data
	nf.NotFound = true
	page404, err := execute(string(layout), "", "notfound", nf)
	if err != nil {
		return nil, fmt.Errorf("template %s: 404.html: %w", name, err)
	}
	return append(out, File{Path: "404.html", Data: page404}), nil
}

// execute renders body (or the named layout block when body is empty)
// with the layout's blocks available. Missing strings are errors.
func execute(layout, body, block string, data page) ([]byte, error) {
	t, err := template.New("page").Option("missingkey=error").Parse(layout)
	if err != nil {
		return nil, err
	}
	if body != "" {
		if t, err = t.New("body").Parse(body); err != nil {
			return nil, err
		}
		block = "body"
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, block, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
