// Package ui serves the web interface on the admin listener: server-rendered
// html/template pages with htmx, embedded fonts and a small script for
// uploads. Every action goes through internal/service.
package ui

import (
	"crypto/rand"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/b0balYson/dropsite/internal/service"
)

//go:embed templates static i18n
var assets embed.FS

// server holds everything the handlers share. It is immutable after New.
type server struct {
	svc      *service.Service
	log      *slog.Logger
	pages    map[string]*pageTemplate
	cats     catalogs
	assetURL map[string]string
	static   map[string]staticFile
	flashKey []byte
	secure   bool
}

// New returns the handler for the admin listener.
func New(svc *service.Service, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s, err := newServer(svc, log)
	if err != nil {
		// Templates, catalogs and assets are embedded at build time; a failure
		// here is a programming error caught by the package tests.
		panic(fmt.Sprintf("ui: %v", err))
	}
	return securityHeaders(s.routes())
}

func newServer(svc *service.Service, log *slog.Logger) (*server, error) {
	cats, err := loadCatalogs()
	if err != nil {
		return nil, err
	}
	static, urls, err := loadStatic()
	if err != nil {
		return nil, err
	}
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate flash key: %w", err)
	}
	return &server{
		svc: svc, log: log, pages: pages, cats: cats, assetURL: urls, static: static,
		flashKey: key, secure: strings.HasPrefix(svc.Config().UIURL, "https://"),
	}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/{file...}", s.serveStatic)

	mux.HandleFunc("GET /login", s.anon(s.loginPage))
	mux.HandleFunc("POST /login", s.anon(s.loginSubmit))
	mux.HandleFunc("POST /logout", s.user(s.logout))
	mux.HandleFunc("GET /invite/{secret}", s.anon(s.invitePage))
	mux.HandleFunc("POST /invite/{secret}", s.anon(s.inviteSubmit))
	mux.HandleFunc("POST /lang", s.anon(s.setLang))

	mux.HandleFunc("GET /{$}", s.user(s.sitesPage))
	mux.HandleFunc("POST /sites", s.user(s.createSite))
	mux.HandleFunc("GET /slug-check", s.user(s.slugCheckNew))
	mux.HandleFunc("GET /sites/{ref}", s.user(s.sitePage))
	mux.HandleFunc("GET /sites/{ref}/history", s.user(s.historyFragment))
	mux.HandleFunc("GET /sites/{ref}/slug-check", s.user(s.slugCheckSite))
	mux.HandleFunc("POST /sites/{ref}/settings", s.user(s.siteSettings))
	mux.HandleFunc("POST /sites/{ref}/access", s.user(s.siteAccess))
	mux.HandleFunc("POST /sites/{ref}/deploy", s.upload(s.deploy))
	mux.HandleFunc("POST /sites/{ref}/rollback", s.user(s.rollback))
	mux.HandleFunc("POST /sites/{ref}/delete", s.user(s.deleteSite))

	mux.HandleFunc("GET /connect", s.user(s.connectPage))
	mux.HandleFunc("POST /connect/tokens", s.user(s.createToken))
	mux.HandleFunc("POST /connect/tokens/{id}/revoke", s.user(s.revokeToken))

	mux.HandleFunc("GET /admin/users", s.admin(s.usersPage))
	mux.HandleFunc("POST /admin/users", s.admin(s.createUser))
	mux.HandleFunc("POST /admin/users/{id}/block", s.admin(s.blockUser))
	mux.HandleFunc("POST /admin/users/{id}/unblock", s.admin(s.unblockUser))
	mux.HandleFunc("POST /admin/users/{id}/invite", s.admin(s.reinviteUser))
	mux.HandleFunc("POST /admin/users/{id}/revoke-tokens", s.admin(s.revokeUserTokens))
	mux.HandleFunc("GET /admin/users/{id}/limits", s.admin(s.userLimitsPage))
	mux.HandleFunc("POST /admin/users/{id}/limits", s.admin(s.saveUserLimits))
	mux.HandleFunc("GET /admin/sites", s.admin(s.allSitesPage))
	mux.HandleFunc("POST /admin/sites/{ref}/apex", s.admin(s.setApex))
	mux.HandleFunc("POST /admin/sites/{ref}/apex/clear", s.admin(s.clearApex))
	mux.HandleFunc("POST /admin/sites/{ref}/transfer", s.admin(s.transferSite))
	mux.HandleFunc("GET /admin/audit", s.admin(s.auditPage))
	mux.HandleFunc("GET /admin/settings", s.admin(s.settingsPage))
	mux.HandleFunc("POST /admin/settings", s.admin(s.saveSettings))

	mux.HandleFunc("/", s.user(s.notFound))
	return mux
}

// csp allows only same-origin resources and no inline code or styles.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
