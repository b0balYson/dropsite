package siteauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
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
	nonceCookie  = brand.Name + "_login_nonce"
	nonceBytes   = 18
	maxFormBytes = 8 << 10
	// The page uses no scripts, inline styles or external resources.
	pageCSP = "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'"
)

var (
	pageTmpl = template.Must(template.ParseFS(assets, "assets/login.html"))
	cssBody  = mustRead("assets/login.css")
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
		h.Set("Content-Security-Policy", pageCSP)
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
	Lang    string
	T       map[string]string
	Title   string
	Next    string
	CSRF    string
	Error   string
	CSSPath string
	Action  string
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
	nonce := currentNonce(r)
	if nonce == "" {
		if nonce, err = newNonce(); err != nil {
			l.fail(w, err)
			return
		}
	}
	token, err := l.svc.VisitorCSRFToken(r.Context(), host, nonce)
	if err != nil {
		l.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: nonceCookie, Value: nonce, Path: Prefix, MaxAge: int(service.VisitorCSRFTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	lang := pickLang(r.Header.Get("Accept-Language"))
	data := pageData{
		Lang: lang, T: catalog[lang], Title: title, Next: next, CSRF: token, Error: errMsg,
		CSSPath: cssPath, Action: LoginPath,
	}
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		l.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// submit checks CSRF and the password, then sets the access cookie.
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
	c, err := l.svc.VisitorLogin(r.Context(), host, r.PostFormValue("password"), l.clientIP(r))
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
	switch de.Kind {
	case domain.KindUnauthorized:
		l.render(w, r, http.StatusUnauthorized, next, t["err.password"])
	case domain.KindRateLimited:
		minutes := int(math.Ceil(de.RetryAfter.Minutes()))
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(de.RetryAfter.Seconds()))))
		l.render(w, r, http.StatusTooManyRequests, next, strings.ReplaceAll(t["err.rate"], "{minutes}", strconv.Itoa(max(minutes, 1))))
	default:
		l.fail(w, err)
	}
}

// checkCSRF requires a same-origin request (when the browser says so) and a
// token bound to the nonce cookie and the host.
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
	if !l.svc.CheckVisitorCSRF(ctx, host, currentNonce(r), r.PostFormValue("_csrf")) {
		return errCSRF
	}
	return nil
}

func (l *login) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	l.log.Error("site login failed", "err", err)
	http.Error(w, "service temporarily unavailable", http.StatusServiceUnavailable)
}

func currentNonce(r *http.Request) string {
	c, err := r.Cookie(nonceCookie)
	if err != nil || len(c.Value) != base64.RawURLEncoding.EncodedLen(nonceBytes) {
		return ""
	}
	if _, err := base64.RawURLEncoding.DecodeString(c.Value); err != nil {
		return ""
	}
	return c.Value
}

func newNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
