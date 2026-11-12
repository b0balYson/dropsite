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
	mux.HandleFunc("GET /sites/{ref}/deploys/{id}", s.user(s.qualityPage))
	mux.HandleFunc("GET /sites/{ref}/slug-check", s.user(s.slugCheckSite))
	mux.HandleFunc("POST /sites/{ref}/settings", s.user(s.siteSettings))
	mux.HandleFunc("POST /sites/{ref}/access", s.user(s.siteAccess))
	mux.HandleFunc("POST /sites/{ref}/publish-settings", s.user(s.sitePublishSettings))
	mux.HandleFunc("POST /sites/{ref}/deploy", s.upload(s.deploy))
	mux.HandleFunc("POST /sites/{ref}/rollback", s.user(s.rollback))
	mux.HandleFunc("POST /sites/from-template", s.user(s.createFromTemplate))
	mux.HandleFunc("POST /sites/{ref}/clone", s.user(s.cloneSite))
	mux.HandleFunc("POST /sites/{ref}/schedules", s.user(s.scheduleRelease))
	mux.HandleFunc("POST /sites/{ref}/schedules/{id}/cancel", s.user(s.cancelSchedule))
	mux.HandleFunc("GET /sites/{ref}/screenshot.png", s.user(s.screenshotPNG))
	mux.HandleFunc("GET /sites/{ref}/compare.png", s.user(s.comparePNG))
	mux.HandleFunc("POST /sites/{ref}/counters", s.user(s.saveCounters))
	mux.HandleFunc("POST /sites/{ref}/delete", s.user(s.deleteSite))
	mux.HandleFunc("POST /sites/{ref}/domains", s.user(s.addDomain))
	mux.HandleFunc("POST /sites/{ref}/domains/remove", s.user(s.removeDomain))
	mux.HandleFunc("GET /sites/{ref}/forms", s.user(s.formsPage))
	mux.HandleFunc("GET /sites/{ref}/forms/export.csv", s.user(s.exportForms))
	mux.HandleFunc("POST /sites/{ref}/forms/settings", s.user(s.formSiteSettings))
	mux.HandleFunc("POST /sites/{ref}/forms/channels", s.user(s.addChannel))
	mux.HandleFunc("POST /sites/{ref}/forms/channels/{id}/verify", s.user(s.verifyChannel))
	mux.HandleFunc("POST /sites/{ref}/forms/channels/{id}/toggle", s.user(s.toggleChannel))
	mux.HandleFunc("POST /sites/{ref}/forms/channels/{id}/delete", s.user(s.deleteChannel))
	mux.HandleFunc("POST /sites/{ref}/forms/channels/{id}/secret", s.user(s.rotateChannel))
	mux.HandleFunc("GET /sites/{ref}/data", s.user(s.collectionsPage))
	mux.HandleFunc("POST /sites/{ref}/data", s.user(s.createCollection))
	mux.HandleFunc("GET /sites/{ref}/data/{name}", s.user(s.collectionPage))
	mux.HandleFunc("POST /sites/{ref}/data/{name}", s.user(s.updateCollection))
	mux.HandleFunc("POST /sites/{ref}/data/{name}/delete", s.user(s.deleteCollection))
	mux.HandleFunc("POST /sites/{ref}/data/{name}/documents", s.user(s.saveDocument))
	mux.HandleFunc("POST /sites/{ref}/data/{name}/documents/delete", s.user(s.deleteDocument))
	mux.HandleFunc("POST /sites/{ref}/data/{name}/moderate", s.user(s.moderateDocuments))
	mux.HandleFunc("GET /sites/{ref}/proxy", s.user(s.proxyPage))
	mux.HandleFunc("POST /sites/{ref}/proxy", s.user(s.saveProxy))
	mux.HandleFunc("GET /sites/{ref}/proxy/{name}/edit", s.user(s.editProxy))
	mux.HandleFunc("POST /sites/{ref}/proxy/{name}/secret", s.user(s.setProxySecret))
	mux.HandleFunc("POST /sites/{ref}/proxy/{name}/secret/delete", s.user(s.clearProxySecret))
	mux.HandleFunc("POST /sites/{ref}/proxy/{name}/delete", s.user(s.deleteProxy))

	s.workspaceRoutes(mux)

	mux.HandleFunc("GET /connect", s.user(s.connectPage))
	mux.HandleFunc("POST /connect/code", s.user(s.createCode))
	mux.HandleFunc("POST /connect/apps/{id}/revoke", s.user(s.revokeApp))
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
	mux.HandleFunc("GET /admin/domains", s.admin(s.adminDomainsPage))
	mux.HandleFunc("GET /admin/domains/{host}/dns", s.admin(s.domainDNS))
	mux.HandleFunc("POST /admin/domains/{host}/approve", s.admin(s.approveDomain))
	mux.HandleFunc("POST /admin/domains/{host}/reject", s.admin(s.rejectDomain))
	mux.HandleFunc("GET /admin/audit", s.admin(s.auditPage))
	mux.HandleFunc("GET /admin/settings", s.admin(s.settingsPage))
	mux.HandleFunc("POST /admin/settings", s.admin(s.saveSettings))
	mux.HandleFunc("POST /admin/settings/forms", s.admin(s.saveFormSettings))

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
