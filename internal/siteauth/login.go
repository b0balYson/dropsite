package siteauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

//go:embed assets
var assets embed.FS

const (
	// nonceCookie carries the CSRF nonce. The __Host- prefix (Secure,
	// Path=/, no Domain) keeps neighbouring sites under the same base domain
	// from planting one; the check also accepts any of several values.
	nonceCookie  = "__Host-" + brand.Name + "_login_nonce"
	nonceBytes   = 18
	maxNonces    = 8
	maxFormBytes = 8 << 10
	// baseCSP covers every response of this handler: no scripts, inline
	// styles or external resources.
	baseCSP = "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'"
)

// pageCSP is the policy of the login page itself. On top of baseCSP the
// Altcha widget needs: its script (self), the challenge fetch (connect-src
// self), its proof-of-work workers, which it starts from blob: URLs, and the
// one <style> element it injects, allowed by a per-response nonce that the
// widget reads from <meta name="csp-nonce">.
func pageCSP(styleNonce string) string {
	return "default-src 'none'; script-src 'self'; worker-src blob:; connect-src 'self'; " +
		"style-src 'self' 'nonce-" + styleNonce + "'; img-src 'self'; form-action 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'"
}

var (
	pageTmpl = template.Must(template.ParseFS(assets, "assets/login.html"))
	cssBody  = mustRead("assets/login.css")
	// cssHref carries a content hash, so the hour-long cache of the
	// stylesheet never outlives an upgrade.
	cssHref = func() string {
		sum := sha256.Sum256(cssBody)
		return cssPath + "?v=" + hex.EncodeToString(sum[:6])
	}()
)

func mustRead(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic("siteauth: " + err.Error())
	}
	return b
}

type login struct {
	svc      *service.Service
	log      *slog.Logger
	clientIP func(*http.Request) string
}

// Login returns the handler for Prefix on the public listener. clientIP
// returns the visitor address (the API middleware resolves trusted proxies).
func Login(svc *service.Service, log *slog.Logger, clientIP func(*http.Request) string) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	l := &login{svc: svc, log: log, clientIP: clientIP}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+LoginPath, l.page)
	mux.HandleFunc("POST "+LoginPath, l.submit)
	mux.HandleFunc("GET "+ChallengePath, l.challenge)
	mux.HandleFunc("GET "+cssPath, serveCSS)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	return headers(mux)
}

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Content-Security-Policy", baseCSP)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin") // keeps Origin on the login POST
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func serveCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(cssBody)
}

type pageData struct {
	Lang          string
	T             map[string]string
	Title         string
	Next          string
	CSRF          string
	Error         string
	CSSPath       string
	Action        string
	StyleNonce    string
	ChallengePath string
	WidgetPath    string
}

// page renders the password form for a protected site's host.
func (l *login) page(w http.ResponseWriter, r *http.Request) {
	l.render(w, r, http.StatusOK, safeNext(r.URL.Query().Get("next")), "")
}

func (l *login) render(w http.ResponseWriter, r *http.Request, status int, next, errMsg string) {
	host := requestHost(r)
	title, err := l.svc.VisitorSite(r.Context(), host)
	if err != nil {
		l.fail(w, err)
		return
	}
	nonce := ""
	if ns := currentNonces(r); len(ns) > 0 {
		nonce = ns[0]
	} else if nonce, err = newNonce(); err != nil {
		l.fail(w, err)
		return
	}
	token, err := l.svc.VisitorCSRFToken(r.Context(), host, nonce)
	if err != nil {
		l.fail(w, err)
		return
	}
	styleNonce, err := newNonce()
	if err != nil {
		l.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: nonceCookie, Value: nonce, Path: "/", MaxAge: int(service.VisitorCSRFTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	lang := pickLang(r.Header.Get("Accept-Language"))
	data := pageData{
		Lang: langTags[lang], T: catalog[lang], Title: title, Next: next, CSRF: token, Error: errMsg,
		CSSPath: cssHref, Action: LoginPath, StyleNonce: styleNonce,
		ChallengePath: ChallengePath, WidgetPath: widgetPath,
	}
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		l.fail(w, err)
		return
	}
	w.Header().Set("Content-Security-Policy", pageCSP(styleNonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// challenge issues the Altcha challenge of the login page as JSON.
func (l *login) challenge(w http.ResponseWriter, r *http.Request) {
	ch, err := l.svc.VisitorAltchaChallenge(r.Context(), requestHost(r), l.clientIP(r))
	if err != nil {
		if de := domain.AsError(err); de.Kind == domain.KindRateLimited {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(de.RetryAfter.Seconds()))))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		l.fail(w, err)
		return
	}
	body, err := json.Marshal(ch)
	if err != nil {
		l.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

// submit checks CSRF, then lets the service check the Altcha solution and
// the password, and sets the access cookie.
func (l *login) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostFormValue("next"))
	t := catalog[pickLang(r.Header.Get("Accept-Language"))]
	host := requestHost(r)
	if err := l.checkCSRF(r.Context(), r, host); err != nil {
		l.render(w, r, http.StatusForbidden, next, t["err.csrf"])
		return
	}
	c, err := l.svc.VisitorLogin(r.Context(), service.VisitorLoginInput{
		Host: host, Password: r.PostFormValue("password"), Altcha: r.PostFormValue("altcha"), IP: l.clientIP(r),
	})
	if err != nil {
		l.loginFailed(w, r, next, t, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: service.VisitorCookieName, Value: c.Value, Path: "/", Expires: c.Expires,
		MaxAge: int(service.VisitorCookieTTL / time.Second), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // next passed safeNext: a local path only
}

func (l *login) loginFailed(w http.ResponseWriter, r *http.Request, next string, t map[string]string, err error) {
	de := domain.AsError(err)
	switch {
	case de.Code == domain.CodeFormCaptcha:
		l.render(w, r, http.StatusBadRequest, next, t["err.altcha"])
	case de.Kind == domain.KindUnauthorized:
		l.render(w, r, http.StatusUnauthorized, next, t["err.password"])
	case de.Kind == domain.KindRateLimited:
		minutes := int(math.Ceil(de.RetryAfter.Minutes()))
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(de.RetryAfter.Seconds()))))
		l.render(w, r, http.StatusTooManyRequests, next, strings.ReplaceAll(t["err.rate"], "{minutes}", strconv.Itoa(max(minutes, 1))))
	default:
		l.fail(w, err)
	}
}

// checkCSRF requires a same-origin request (when the browser says so) and a
// token bound to one of the nonce cookies and the host.
func (l *login) checkCSRF(ctx context.Context, r *http.Request, host string) error {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return errCSRF
	}
	// Browsers send "Origin: null" for a same-origin POST from a page with a
	// strict referrer policy; accept it only when Fetch Metadata vouches for it.
	origin := r.Header.Get("Origin")
	sameOriginNull := origin == "null" && r.Header.Get("Sec-Fetch-Site") == "same-origin"
	if origin != "" && origin != "https://"+r.Host && !sameOriginNull {
		return errCSRF
	}
	token := r.PostFormValue("_csrf")
	for _, nonce := range currentNonces(r) {
		if l.svc.CheckVisitorCSRF(ctx, host, nonce, token) {
			return nil
		}
	}
	return errCSRF
}

func (l *login) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	l.log.Error("site login failed", "err", err)
	http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
}

// currentNonces returns the well-formed nonce cookie values of the request
// (at most maxNonces). Several may arrive if someone planted an extra one;
// none of them is trusted on its own, the CSRF token must match.
func currentNonces(r *http.Request) []string {
	var out []string
	for _, c := range r.CookiesNamed(nonceCookie) {
		if len(out) == maxNonces {
			break
		}
		if len(c.Value) != base64.RawURLEncoding.EncodedLen(nonceBytes) {
			continue
		}
		if _, err := base64.RawURLEncoding.DecodeString(c.Value); err != nil {
			continue
		}
		out = append(out, c.Value)
	}
	return out
}

func newNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
