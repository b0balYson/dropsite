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
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s, err := newServer(svc, log)
	if err != nil {
		panic(fmt.Sprintf("ui: %v", err))
	}
	s.secure = true // the API host is always served over HTTPS by Caddy
	urls := make(map[string]string, len(s.assetURL))
	for k, v := range s.assetURL {
		urls[k] = authorizeStatic + strings.TrimPrefix(v, "/static/")
	}
	s.assetURL = urls
	a := &authorizer{s: s, az: az, ip: clientIP}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+authorizeStatic+"{file...}", s.serveStatic)
	mux.HandleFunc("GET "+oauth.AuthorizePath, a.show)
	mux.HandleFunc("POST "+oauth.AuthorizePath, a.submit)
	return securityHeaders(mux)
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
	// Action is where the consent form posts (the API host by default).
	Action string
	// Link is true when the request came through a connect URL whose code
	// is still valid: the page asks only for consent, not for a code.
	Link bool
	// SessionEmail is set on the public admin host: the signed-in user
	// approves without a code.
	SessionEmail string
	// AdminURL opens the same request on the public admin host (while public
	// access is on), where a signed-in user approves without a code.
	AdminURL string
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
	a.s.renderConsent(w, r, rc, status, d, formAction)
}

// renderConsent renders the consent page; redirectURI is the client's
// redirect, allowed as a form target.
func (s *server) renderConsent(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, d authorizeData, redirectURI string) {
	w.Header().Set("Content-Security-Policy", consentCSP(redirectURI))
	langLinks(r, &d)
	v := s.newView(r, rc, "", "oauth.title", d)
	s.execute(w, status, s.pages["authorize"], "authorize_page", v)
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

// consentData describes a pending request for the consent page.
func consentData(az *oauth.Server, p oauth.Pending) authorizeData {
	d := authorizeData{
		Sealed: az.Seal(p), ClientName: p.Client.Name, RedirectHost: service.RedirectHost(p.Request.RedirectURI),
		Action: oauth.AuthorizePath, Link: p.LinkCode != "",
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

// data is consentData plus the way to approve on the public admin host
// while public access is on. That link never carries a connect code.
func (a *authorizer) data(r *http.Request, p oauth.Pending) authorizeData {
	d := consentData(a.az, p)
	if !d.Link && a.s.svc.PublicAdminEnabled(r.Context()) {
		p.LinkCode = ""
		d.AdminURL = a.s.svc.PublicAdminURL() + sessionAuthorizePath + "?" + url.Values{"request": {a.az.Seal(p)}}.Encode()
	}
	return d
}

// linkFailed is the page after a connect URL that cannot authorize (unknown,
// used or expired code, or too many failures): the usual code field with a
// message that does not tell which.
func (a *authorizer) linkFailed(w http.ResponseWriter, r *http.Request, rc *reqCtx, p oauth.Pending, err error) {
	p.LinkCode = ""
	d := a.data(r, p)
	d.Error = rc.loc.T("oauth.link.invalid")
	if domain.AsError(err).Kind == domain.KindRateLimited {
		d.Error, _ = a.s.describe(rc, err)
		w.Header().Set("Retry-After", retryAfter(err))
	}
	a.render(w, r, rc, http.StatusOK, d, p.Request.RedirectURI)
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
	if p.LinkCode != "" {
		if err := a.s.svc.CheckConnectLink(r.Context(), p.LinkCode, rc.ip); err != nil {
			if domain.AsError(err).Kind == domain.KindInternal {
				msg, status := a.s.describe(rc, err)
				a.render(w, r, rc, status, authorizeData{Fatal: msg}, "")
				return
			}
			a.linkFailed(w, r, rc, p, err)
			return
		}
	}
	a.render(w, r, rc, http.StatusOK, a.data(r, p), p.Request.RedirectURI)
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
	var code string
	if p.LinkCode != "" {
		code, err = a.s.svc.AuthorizeOAuthLink(r.Context(), p.Request, p.LinkCode, rc.ip)
	} else {
		code, err = a.s.svc.AuthorizeOAuth(r.Context(), p.Request, r.PostFormValue("code"), rc.ip)
	}
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		a.redirectOut(w, r, p, a.az.ErrorURL(&oauth.RedirectError{
			RedirectURI: p.Request.RedirectURI, State: p.State, Code: oe.Code, Description: oe.Description,
		}))
		return
	}
	if err != nil && p.LinkCode != "" && domain.AsError(err).Kind != domain.KindInternal {
		// The link was used meanwhile (another tab) or expired: fall back
		// to the code field.
		a.linkFailed(w, r, rc, p, err)
		return
	}
	if err != nil {
		d := a.data(r, p)
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
	consentRedirect(w, r, p, target)
}

// consentRedirect sends the browser back to the client after the consent.
func consentRedirect(w http.ResponseWriter, r *http.Request, p oauth.Pending, target string) {
	w.Header().Set("Content-Security-Policy", consentCSP(p.Request.RedirectURI))
	http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: target is the client's registered redirect URI
}

func retryAfter(err error) string {
	secs := int(domain.AsError(err).RetryAfter.Seconds() + 0.999)
	return fmt.Sprint(max(secs, 1))
}
