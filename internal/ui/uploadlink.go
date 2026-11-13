package ui

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// UploadLinkPattern is where the upload page is mounted on the API host.
const UploadLinkPattern = "GET " + service.UploadLinkPath + "{token}"

// uploadCSP is the UI policy for the upload page: scripts and styles only
// from the API host, requests only back to it, no forms anywhere.
const uploadCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; form-action 'none'; base-uri 'none'"

// The upload link page (spec upload-link) is served by NewAPIHostPages. It
// sets no cookies: the token in the address is the only credential, and it
// grants uploads into one upload session.

// apiHostServer is a UI server whose asset URLs point at the static path
// served on the API host (authorizeStatic).
func apiHostServer(svc *service.Service, log *slog.Logger) (*server, error) {
	s, err := newServer(svc, log)
	if err != nil {
		return nil, err
	}
	s.secure = true // the API host is always served over HTTPS by Caddy
	urls := make(map[string]string, len(s.assetURL))
	for k, v := range s.assetURL {
		urls[k] = authorizeStatic + strings.TrimPrefix(v, "/static/")
	}
	s.assetURL = urls
	return s, nil
}

type uploadPage struct {
	s  *server
	ip func(*http.Request) string
}

// serve answers GET /u/{token}; deeper paths are not pages.
func (p *uploadPage) serve(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc(UploadLinkPattern, p.show)
	mux.ServeHTTP(w, r)
}

type uploadData struct {
	Info  service.LinkInfo
	Fatal string
	Langs []langLink
	// Limits is the human summary of the owner's limits.
	Limits string
}

func (p *uploadPage) show(w http.ResponseWriter, r *http.Request) {
	rc := &reqCtx{ip: r.RemoteAddr}
	if p.ip != nil {
		rc.ip = p.ip(r)
	}
	lang := r.URL.Query().Get("ui_lang")
	if !supportedLang(lang) {
		lang = acceptLanguage(r.Header.Get("Accept-Language"))
	}
	if !supportedLang(lang) {
		lang = languages[0]
	}
	rc.loc = p.s.cats.locale(lang)
	h := w.Header()
	h.Set("Content-Security-Policy", uploadCSP)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")

	d := uploadData{}
	status := http.StatusOK
	info, err := p.s.svc.UploadLinkInfo(r.Context(), r.PathValue("token"), rc.ip)
	switch e := domain.AsError(err); {
	case err == nil:
		d.Info = info
		mb := rc.loc.T("upload.mb")
		d.Limits = rc.loc.T("upload.limits", "total", humanSize(info.MaxBytes, mb), "file", humanSize(info.MaxFileBytes, mb),
			"files", info.MaxFiles)
	case e.Kind == domain.KindRateLimited:
		h.Set("Retry-After", retryAfter(err))
		status, d.Fatal = http.StatusTooManyRequests, rc.loc.T("upload.error.busy")
	case e.Code == domain.CodeUploadLinkInvalid:
		status, d.Fatal = http.StatusNotFound, rc.loc.T("upload.error.expired")
	default:
		p.s.log.Error("upload page failed", "err", err)
		status, d.Fatal = http.StatusInternalServerError, rc.loc.T("upload.error.internal")
	}
	d.Langs = uploadLangLinks(r)
	v := p.s.newView(r, rc, "", "upload.title", d)
	v.Path = ""
	p.s.execute(w, status, p.s.pages["upload_link"], "upload_page", v)
}

// uploadLangLinks switch the language of this page and keep nothing else.
func uploadLangLinks(r *http.Request) []langLink {
	links := make([]langLink, 0, len(langOptions))
	for _, o := range langOptions {
		links = append(links, langLink{langOption: o, Href: r.URL.Path + "?ui_lang=" + o.Code})
	}
	return links
}

// humanSize spells a byte limit in megabytes (unit: the localized "MB").
func humanSize(n int64, unit string) string {
	switch {
	case n <= 0:
		return "∞"
	case n%(1<<20) == 0:
		return fmt.Sprintf("%d %s", n>>20, unit)
	}
	return fmt.Sprintf("%.2f %s", float64(n)/(1<<20), unit)
}
