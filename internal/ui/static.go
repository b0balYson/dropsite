package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

type staticFile struct {
	body  []byte
	ctype string
	etag  string
}

// loadStatic reads embedded assets and derives versioned URLs so browsers can
// cache them forever and still pick up new builds.
func loadStatic() (map[string]staticFile, map[string]string, error) {
	files := map[string]staticFile{}
	urls := map[string]string{}
	err := fs.WalkDir(assets, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := assets.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		name := strings.TrimPrefix(p, "static/")
		sum := sha256.Sum256(body)
		version := hex.EncodeToString(sum[:6])
		ctype := mime.TypeByExtension(path.Ext(name))
		switch path.Ext(name) {
		case ".woff2":
			ctype = "font/woff2"
		case ".ico":
			ctype = "image/x-icon"
		case ".txt":
			ctype = "text/plain; charset=utf-8"
		case ".js":
			ctype = "text/javascript; charset=utf-8"
		case ".css":
			ctype = "text/css; charset=utf-8"
		}
		files[name] = staticFile{body: body, ctype: ctype, etag: `"` + version + `"`}
		urls[name] = "/static/" + name + "?v=" + version
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("load static assets: %w", err)
	}
	return files, urls, nil
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, r, r.PathValue("file"))
}

// serveFavicon answers browsers that ask for /favicon.ico without reading
// the page's icon links.
func (s *server) serveFavicon(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, r, "brand/favicon.ico")
}

func (s *server) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, ok := s.static[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("ETag", f.etag)
	if r.URL.Query().Get("v") != "" {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if r.Header.Get("If-None-Match") == f.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(f.body)
}
