// Package api serves the public HTTP API v1 (see docs/api-contract.md).
// It is a thin adapter: every decision about access is made by
// internal/service.
package api

import (
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/siteauth"
)

// Prefix is the base path of API v1.
const Prefix = "/api/v1"

// handlerFunc is an endpoint; a returned error is written in the API format.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// route is one endpoint. Path is relative to Prefix and uses ServeMux
// wildcards ({ref}, {id}).
type route struct {
	Method string
	Path   string
	Public bool // no bearer token required
	handle handlerFunc
}

type api struct {
	svc *service.Service
	log *slog.Logger
}

// New returns the handler for the public listener. Mounts add non-API paths
// (MCP, OAuth) behind the same middleware.
func New(svc *service.Service, log *slog.Logger, mounts ...Mount) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a := &api{svc: svc, log: log}
	mux := http.NewServeMux()
	for _, m := range mounts {
		mux.Handle(m.Pattern, m.Handler)
	}
	a.register(mux, a.routes())
	// The visitor login page of protected sites: Caddy proxies /_dropsite/*
	// on site hosts here. On the API host it answers 404 (no such site).
	mux.Handle(siteauth.Prefix, siteauth.Login(svc, log, func(r *http.Request) string {
		if info := infoFrom(r.Context()); info != nil {
			return info.ip
		}
		return r.RemoteAddr
	}))
	a.registerSitePaths(mux)
	a.registerDataPaths(mux)
	a.registerProxyPath(mux)
	return a.middleware(mux)
}

// routes is the single table of endpoints; the OpenAPI test compares the
// document against it.
func (a *api) routes() []route {
	return append([]route{
		{Method: http.MethodGet, Path: "/openapi.json", Public: true, handle: a.openAPI},
		{Method: http.MethodPost, Path: "/connect/exchange", Public: true, handle: a.exchangeConnect},
		{Method: http.MethodGet, Path: "/me", handle: a.me},
		{Method: http.MethodGet, Path: "/sites", handle: a.listSites},
		{Method: http.MethodPost, Path: "/sites", handle: a.createSite},
		{Method: http.MethodGet, Path: "/sites/{ref}", handle: a.getSite},
		{Method: http.MethodPatch, Path: "/sites/{ref}", handle: a.updateSite},
		{Method: http.MethodDelete, Path: "/sites/{ref}", handle: a.deleteSite},
		{Method: http.MethodPost, Path: "/sites/{ref}/deploys", handle: a.deploy},
		{Method: http.MethodGet, Path: "/sites/{ref}/deploys", handle: a.listDeploys},
		{Method: http.MethodGet, Path: "/sites/{ref}/deploys/{id}", handle: a.getDeploy},
		{Method: http.MethodPost, Path: "/sites/{ref}/rollback", handle: a.rollback},
		{Method: http.MethodGet, Path: "/sites/{ref}/publish-settings", handle: a.getPublishSettings},
		{Method: http.MethodPatch, Path: "/sites/{ref}/publish-settings", handle: a.updatePublishSettings},
		{Method: http.MethodGet, Path: "/sites/{ref}/hostnames", handle: a.listHostnames},
		{Method: http.MethodPost, Path: "/sites/{ref}/hostnames", handle: a.addHostname},
		{Method: http.MethodDelete, Path: "/sites/{ref}/hostnames/{hostname}", handle: a.removeHostname},
		{Method: http.MethodGet, Path: "/sites/{ref}/domain-setup", handle: a.domainSetup},
		{Method: http.MethodGet, Path: "/sites/{ref}/stats", handle: a.siteStats},
		{Method: http.MethodGet, Path: "/sites/{ref}/attachments", handle: a.getSiteAttachments},
		{Method: http.MethodGet, Path: "/sites/{ref}/counters", handle: a.getCounters},
		{Method: http.MethodPatch, Path: "/sites/{ref}/counters", handle: a.updateCounters},
		{Method: http.MethodGet, Path: "/sites/{ref}/files", handle: a.listSiteFiles},
		{Method: http.MethodPatch, Path: "/sites/{ref}/files", handle: a.updateSiteFiles},
		{Method: http.MethodPost, Path: "/sites/{ref}/files/read", handle: a.readSiteFiles},
		{Method: http.MethodPost, Path: "/uploads", handle: a.beginUpload},
		{Method: http.MethodPost, Path: "/uploads/{id}/files", handle: a.uploadFiles},
		{Method: http.MethodPost, Path: "/uploads/{id}/commit", handle: a.commitUpload},
		{Method: http.MethodGet, Path: "/sites/{ref}/forms/channels", handle: a.listChannels},
		{Method: http.MethodPost, Path: "/sites/{ref}/forms/channels", handle: a.createChannel},
		{Method: http.MethodPatch, Path: "/sites/{ref}/forms/channels/{id}", handle: a.updateChannel},
		{Method: http.MethodDelete, Path: "/sites/{ref}/forms/channels/{id}", handle: a.deleteChannel},
		{Method: http.MethodPost, Path: "/sites/{ref}/forms/channels/{id}/verify", handle: a.verifyChannel},
		{Method: http.MethodPost, Path: "/sites/{ref}/forms/channels/{id}/secret", handle: a.rotateChannelSecret},
		{Method: http.MethodGet, Path: "/sites/{ref}/forms/settings", handle: a.getFormSiteSettings},
		{Method: http.MethodPatch, Path: "/sites/{ref}/forms/settings", handle: a.updateFormSiteSettings},
		{Method: http.MethodGet, Path: "/sites/{ref}/forms/submissions", handle: a.listSubmissions},
		{Method: http.MethodGet, Path: "/sites/{ref}/forms/submissions.csv", handle: a.exportSubmissions},
		{Method: http.MethodGet, Path: "/sites/{ref}/forms/submissions/search", handle: a.searchSubmissions},
		{Method: http.MethodGet, Path: "/sites/{ref}/forms/submissions/export", handle: a.exportSubmissionsText},
		{Method: http.MethodGet, Path: "/sites/{ref}/collections", handle: a.listCollections},
		{Method: http.MethodPost, Path: "/sites/{ref}/collections", handle: a.createCollection},
		{Method: http.MethodPatch, Path: "/sites/{ref}/collections/{name}", handle: a.updateCollection},
		{Method: http.MethodDelete, Path: "/sites/{ref}/collections/{name}", handle: a.deleteCollection},
		{Method: http.MethodGet, Path: "/sites/{ref}/collections/{name}/documents", handle: a.listDocuments},
		{Method: http.MethodPost, Path: "/sites/{ref}/collections/{name}/documents", handle: a.saveDocuments},
		{Method: http.MethodPost, Path: "/sites/{ref}/collections/{name}/delete-documents", handle: a.deleteDocuments},
		{Method: http.MethodDelete, Path: "/sites/{ref}/collections/{name}/documents/{doc}", handle: a.deleteDocument},
		{Method: http.MethodPost, Path: "/sites/{ref}/collections/{name}/moderate", handle: a.moderateDocuments},
		{Method: http.MethodGet, Path: "/sites/{ref}/proxy/endpoints", handle: a.listProxyEndpoints},
		{Method: http.MethodPut, Path: "/sites/{ref}/proxy/endpoints/{name}", handle: a.putProxyEndpoint},
		{Method: http.MethodDelete, Path: "/sites/{ref}/proxy/endpoints/{name}", handle: a.deleteProxyEndpoint},
		{Method: http.MethodGet, Path: "/tokens", handle: a.listTokens},
		{Method: http.MethodPost, Path: "/tokens", handle: a.createToken},
		{Method: http.MethodDelete, Path: "/tokens/{id}", handle: a.revokeToken},
		{Method: http.MethodGet, Path: "/connections", handle: a.listConnections},
		{Method: http.MethodDelete, Path: "/connections/{kind}/{id}", handle: a.revokeConnection},
		{Method: http.MethodPut, Path: "/root-site", handle: a.setRootSite},
		{Method: http.MethodDelete, Path: "/root-site", handle: a.clearRootSite},
		{Method: http.MethodGet, Path: "/admin/users", handle: adminOnly(a.listUsers)},
		{Method: http.MethodPost, Path: "/admin/users", handle: adminOnly(a.createUser)},
		{Method: http.MethodGet, Path: "/admin/users/{id}", handle: adminOnly(a.getUser)},
		{Method: http.MethodPatch, Path: "/admin/users/{id}", handle: adminOnly(a.updateUser)},
		{Method: http.MethodGet, Path: "/admin/sites", handle: adminOnly(a.listAllSites)},
		{Method: http.MethodPost, Path: "/admin/sites/{ref}/apex", handle: adminOnly(a.setApex)},
		{Method: http.MethodDelete, Path: "/admin/sites/{ref}/apex", handle: adminOnly(a.clearApex)},
		{Method: http.MethodPost, Path: "/admin/sites/{ref}/transfer", handle: adminOnly(a.transfer)},
		{Method: http.MethodGet, Path: "/admin/audit", handle: adminOnly(a.listAudit)},
		{Method: http.MethodGet, Path: "/admin/domains", handle: adminOnly(a.listDomains)},
		{Method: http.MethodPost, Path: "/admin/domains/{hostname}/approve", handle: adminOnly(a.approveDomain)},
		{Method: http.MethodPost, Path: "/admin/domains/{hostname}/reject", handle: adminOnly(a.rejectDomain)},
		{Method: http.MethodPost, Path: "/admin/domains/{hostname}/dns-check", handle: adminOnly(a.checkDomainDNS)},
		{Method: http.MethodGet, Path: "/admin/forms/settings", handle: adminOnly(a.getFormSettings)},
		{Method: http.MethodPatch, Path: "/admin/forms/settings", handle: adminOnly(a.updateFormSettings)},
	}, a.workflowRoutes()...)
}

// register mounts every route plus, per path, a method-less fallback that
// answers 405 in the API format, and a catch-all 404.
func (a *api) register(mux *http.ServeMux, routes []route) {
	allowed := map[string][]string{}
	for _, rt := range routes {
		mux.Handle(rt.Method+" "+Prefix+rt.Path, a.endpoint(rt))
		allowed[rt.Path] = append(allowed[rt.Path], rt.Method)
	}
	for path, methods := range allowed {
		mux.Handle(Prefix+path, a.methodNotAllowed(path, methods))
	}
	mux.Handle("/", a.wrap("", func(http.ResponseWriter, *http.Request) error { return errRouteNotFound }))
}

// endpoint wraps a route with authentication and error writing.
func (a *api) endpoint(rt route) http.Handler {
	h := rt.handle
	if !rt.Public {
		h = a.authenticated(h)
	}
	return a.wrap(rt.Method+" "+rt.Path, h)
}

func (a *api) methodNotAllowed(path string, methods []string) http.Handler {
	allow := append([]string(nil), methods...)
	if slices.Contains(allow, http.MethodGet) {
		allow = append(allow, http.MethodHead)
	}
	sort.Strings(allow)
	header := strings.Join(allow, ", ")
	return a.wrap(path, func(w http.ResponseWriter, _ *http.Request) error {
		w.Header().Set("Allow", header)
		return errMethodNotAllowed
	})
}

// wrap records the route template for logging and writes returned errors.
func (a *api) wrap(pattern string, h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := infoFrom(r.Context()); info != nil && pattern != "" {
			info.route = pattern
		}
		if err := h(w, r); err != nil {
			a.writeError(w, r, err)
		}
	})
}
