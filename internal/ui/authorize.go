package ui

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/oauth"
	"github.com/b0balYson/dropsite/internal/service"
)

// authorizeStatic is where the consent page loads CSS and fonts on the API host.
const authorizeStatic = "/oauth/static/"

// NewAuthorize returns the OAuth consent page for the public API host. It
// never asks for a password (passwords are entered only in the web
// interface, on its own listeners): the user proves their identity with a connect code issued in
// the interface. clientIP returns the address resolved by the API middleware.
func NewAuthorize(svc *service.Service, az *oauth.Server, clientIP func(*http.Request) string, log *slog.Logger) http.Handler {
	return NewAPIHostPages(svc, az, clientIP, log)
}

// NewAPIHostPages returns the pages of the public API host sharing one set
// of templates and assets: the OAuth consent page, its static files and the
// upload link page (mount it on authorizeStatic, oauth.AuthorizePath and
// UploadLinkPattern).
func NewAPIHostPages(svc *service.Service, az *oauth.Server, clientIP func(*http.Request) string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s, err := apiHostServer(svc, log)
	if err != nil {
		panic(fmt.Sprintf("ui: %v", err))
	}
	a := &authorizer{s: s, az: az, ip: clientIP}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+authorizeStatic+"{file...}", s.serveStatic)
	mux.HandleFunc("GET "+oauth.AuthorizePath, a.show)
	mux.HandleFunc("POST "+oauth.AuthorizePath, a.submit)
	consent := securityHeaders(mux)
	page := &uploadPage{s: s, ip: clientIP}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if strings.HasPrefix(r.URL.Path, service.UploadLinkPath) {
				page.serve(w, r)
				return
			}
		}
		consent.ServeHTTP(w, r)
	})
}

type authorizer struct {
	s  *server
	az *oauth.Server
	ip func(*http.Request) string
}

type authorizeData struct {
	Sealed       string
	ClientName   string
	ClientHost   string
	RedirectHost string
	Loopback     bool
	Error        string
	Fatal        string
	Langs        []langLink
}

// langLink is one language switch link that keeps the request.
type langLink struct {
	langOption
	Href string
}

// langLinks keeps the authorization request when switching the language.
func langLinks(r *http.Request, d *authorizeData) {
	if r.Method != http.MethodGet {
		return
	}
	links := make([]langLink, 0, len(langOptions))
	for _, o := range langOptions {
		q := r.URL.Query()
		q.Set("ui_lang", o.Code)
		links = append(links, langLink{langOption: o, Href: r.URL.Path + "?" + q.Encode()})
	}
	d.Langs = links
}

func (a *authorizer) begin(w http.ResponseWriter, r *http.Request) *reqCtx {
	rc := &reqCtx{ip: r.RemoteAddr}
	if a.ip != nil {
		rc.ip = a.ip(r)
	}
	lang := r.URL.Query().Get("ui_lang")
	if !supportedLang(lang) {
		lang = a.s.pickLang(r, rc)
	}
	rc.loc = a.s.cats.locale(lang)
	rc.csrf = a.s.anonCSRF(w, r)
	return rc
}

func (a *authorizer) render(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, d authorizeData, formAction string) {
	w.Header().Set("Content-Security-Policy", consentCSP(formAction))
	langLinks(r, &d)
	v := a.s.newView(r, rc, "", "oauth.title", d)
	a.s.execute(w, status, a.s.pages["authorize"], "authorize_page", v)
}

// consentCSP is the UI policy with the client's redirect origin allowed as a
// form target: browsers apply form-action to the redirect after the POST.
// Only a validated origin (scheme, DNS name or IP literal, port) is added.
func consentCSP(redirectURI string) string {
	if origin := service.RedirectOrigin(redirectURI); origin != "" {
		return strings.Replace(csp, "form-action 'self'", "form-action 'self' "+origin, 1)
	}
	return csp
}

func (a *authorizer) fatal(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, msgKey string) {
	a.render(w, r, rc, status, authorizeData{Fatal: rc.loc.T(msgKey)}, "")
}

func (a *authorizer) data(p oauth.Pending) authorizeData {
	d := authorizeData{
		Sealed: a.az.Seal(p), ClientName: p.Client.Name, RedirectHost: service.RedirectHost(p.Request.RedirectURI),
	}
	if p.Client.Metadata {
		d.ClientHost = service.RedirectHost(p.Client.ID)
	}
	if u, err := url.Parse(p.Request.RedirectURI); err == nil {
		h := u.Hostname()
		ip := net.ParseIP(h)
		d.Loopback = strings.EqualFold(h, "localhost") || (ip != nil && ip.IsLoopback())
	}
	return d
}

func (a *authorizer) show(w http.ResponseWriter, r *http.Request) {
	rc := a.begin(w, r)
	p, err := a.az.ValidateAuthorize(r.Context(), r.URL.Query(), rc.ip)
	var re *oauth.RedirectError
	if errors.As(err, &re) {
		http.Redirect(w, r, a.az.ErrorURL(re), http.StatusFound)
		return
	}
	if errors.Is(err, domain.ErrRateLimited) {
		msg, status := a.s.describe(rc, err)
		w.Header().Set("Retry-After", retryAfter(err))
		a.render(w, r, rc, status, authorizeData{Fatal: msg}, "")
		return
	}
	if err != nil {
		a.fatal(w, r, rc, http.StatusBadRequest, "oauth.error.request")
		return
	}
	a.render(w, r, rc, http.StatusOK, a.data(p), p.Request.RedirectURI)
}

func (a *authorizer) submit(w http.ResponseWriter, r *http.Request) {
	rc := a.begin(w, r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if !sameOrigin(r) || !tokensEqual(rc.csrf, r.PostFormValue(csrfField)) {
		a.fatal(w, r, rc, http.StatusForbidden, "oauth.error.form")
		return
	}
	p, err := a.az.Open(r.PostFormValue("request"))
	if err != nil {
		a.fatal(w, r, rc, http.StatusBadRequest, "oauth.error.form")
		return
	}
	if r.PostFormValue("action") == "deny" {
		a.redirectOut(w, r, p, a.az.ErrorURL(&oauth.RedirectError{
			RedirectURI: p.Request.RedirectURI, State: p.State, Code: "access_denied", Description: "the user denied access",
		}))
		return
	}
	code, err := a.s.svc.AuthorizeOAuth(r.Context(), p.Request, r.PostFormValue("code"), rc.ip)
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		a.redirectOut(w, r, p, a.az.ErrorURL(&oauth.RedirectError{
			RedirectURI: p.Request.RedirectURI, State: p.State, Code: oe.Code, Description: oe.Description,
		}))
		return
	}
	if err != nil {
		d := a.data(p)
		msg, status := a.s.describe(rc, err)
		if domain.AsError(err).Kind == domain.KindRateLimited {
			w.Header().Set("Retry-After", retryAfter(err))
		}
		d.Error = msg
		a.render(w, r, rc, status, d, p.Request.RedirectURI)
		return
	}
	a.redirectOut(w, r, p, a.az.SuccessURL(p, code))
}

func (a *authorizer) redirectOut(w http.ResponseWriter, r *http.Request, p oauth.Pending, target string) {
	w.Header().Set("Content-Security-Policy", consentCSP(p.Request.RedirectURI))
	http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: target is the client's registered redirect URI
}

func retryAfter(err error) string {
	secs := int(domain.AsError(err).RetryAfter.Seconds() + 0.999)
	return fmt.Sprint(max(secs, 1))
}
