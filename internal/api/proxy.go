package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// proxyUsageDTO are the usage counters of an endpoint.
type proxyUsageDTO struct {
	Requests   int64      `json:"requests"`
	Errors     int64      `json:"errors"`
	Rejected   int64      `json:"rejected"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastStatus *int       `json:"last_status"`
	// Today counts the calls of the current UTC day; Paused tells the
	// daily budget ran out.
	Today  int  `json:"today"`
	Paused bool `json:"paused"`
}

// proxyLimitsDTO are per-endpoint limits; in a request 0 (or absent) keeps
// the class default.
type proxyLimitsDTO struct {
	PerMinute    int `json:"per_minute"`
	DailyBudget  int `json:"daily_budget"`
	SessionQuota int `json:"session_quota"`
}

func fromLimits(l domain.ProxyLimits) proxyLimitsDTO {
	return proxyLimitsDTO{PerMinute: l.PerMinute, DailyBudget: l.DailyBudget, SessionQuota: l.SessionQuota}
}

func (l *proxyLimitsDTO) toDomain() domain.ProxyLimits {
	if l == nil {
		return domain.ProxyLimits{}
	}
	return domain.ProxyLimits{PerMinute: l.PerMinute, DailyBudget: l.DailyBudget, SessionQuota: l.SessionQuota}
}

// ProxyTransparentWarning is shown with every endpoint of the transparent
// class.
const ProxyTransparentWarning = "transparent: anyone who opens the site can call this API with the key for any " +
	"allowed method and path; use it only on a password-protected site, otherwise choose the class ai, send or read"

func proxyWarning(c domain.ProxyClass) string {
	if c == domain.ProxyClassTransparent {
		return ProxyTransparentWarning
	}
	return ""
}

// proxyEndpointDTO is an endpoint without its secret.
type proxyEndpointDTO struct {
	Name            string        `json:"name"`
	UpstreamURL     string        `json:"upstream_url"`
	AllowedHosts    []string      `json:"allowed_hosts"`
	Methods         []string      `json:"methods"`
	Paths           []string      `json:"paths"`
	Auth            string        `json:"auth"`
	AuthName        *string       `json:"auth_name"`
	Enabled         bool          `json:"enabled"`
	SecretSet       bool          `json:"secret_set"`
	SecretUpdatedAt *time.Time    `json:"secret_updated_at"`
	SecretURL       string        `json:"secret_url"`
	CallURL         string        `json:"call_url"`
	Usage           proxyUsageDTO `json:"usage"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	Class           string        `json:"class"`
	// Template is the request template of the classes ai and send.
	Template        *domain.ProxyTemplate `json:"template"`
	Limits          proxyLimitsDTO        `json:"limits"`
	EffectiveLimits proxyLimitsDTO        `json:"effective_limits"`
	Warning         *string               `json:"warning"`
}

func toProxyEndpoint(v service.ProxyEndpointView) proxyEndpointDTO {
	e := v.Endpoint
	out := proxyEndpointDTO{
		Name: e.Name, UpstreamURL: e.UpstreamURL, AllowedHosts: nonNil(e.AllowedHosts), Methods: nonNil(e.Methods),
		Paths: nonNil(e.Paths), Auth: string(e.Auth), AuthName: optional(e.AuthName), Enabled: e.Enabled,
		SecretSet: v.SecretSet, SecretUpdatedAt: e.SecretUpdatedAt, SecretURL: v.SecretURL, CallURL: v.CallURL,
		Usage: proxyUsageDTO{Requests: e.Usage.Requests, Errors: e.Usage.Errors, Rejected: e.Usage.Rejected,
			LastUsedAt: e.Usage.LastUsedAt, Today: e.Usage.Today, Paused: e.Usage.Paused},
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		Class: string(e.Class), Template: e.Template, Limits: fromLimits(e.Limits),
		EffectiveLimits: fromLimits(v.Effective), Warning: optional(proxyWarning(e.Class)),
	}
	if e.Usage.LastStatus != 0 {
		st := e.Usage.LastStatus
		out.Usage.LastStatus = &st
	}
	return out
}

// proxyEndpointRequest is the body of PUT .../proxy/endpoints/{name}.
// Secret is declared only to refuse it: keys are entered in the web
// interface.
type proxyEndpointRequest struct {
	UpstreamURL  string          `json:"upstream_url"`
	AllowedHosts []string        `json:"allowed_hosts"`
	Methods      []string        `json:"methods"`
	Paths        []string        `json:"paths"`
	Auth         string          `json:"auth"`
	AuthName     string          `json:"auth_name"`
	Enabled      *bool           `json:"enabled"`
	Secret       json.RawMessage `json:"secret"`
	// Class is omitted to keep the class of an existing endpoint; a new
	// endpoint without one is "ai".
	Class    string                `json:"class"`
	Template *domain.ProxyTemplate `json:"template"`
	Limits   *proxyLimitsDTO       `json:"limits"`
}

func (a *api) listProxyEndpoints(w http.ResponseWriter, r *http.Request) error {
	eps, err := a.svc.ListProxyEndpoints(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	items := make([]proxyEndpointDTO, 0, len(eps))
	for _, e := range eps {
		items = append(items, toProxyEndpoint(e))
	}
	writeJSON(w, http.StatusOK, newList(items, len(items)))
	return nil
}

func (a *api) putProxyEndpoint(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req proxyEndpointRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Secret != nil {
		// Resolve the site first so a foreign site stays a 404.
		v, err := a.svc.GetSite(ctx, actorFrom(ctx), r.PathValue("ref"))
		if err != nil {
			return err
		}
		return a.svc.ErrProxySecretUIOnly(v.Site.ID, r.PathValue("name"))
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	view, created, err := a.svc.PutProxyEndpoint(ctx, actorFrom(ctx), r.PathValue("ref"), r.PathValue("name"),
		domain.ProxyConfig{
			UpstreamURL: req.UpstreamURL, AllowedHosts: req.AllowedHosts, Methods: req.Methods, Paths: req.Paths,
			Auth: domain.ProxyAuth(req.Auth), AuthName: req.AuthName, Enabled: enabled,
			Class: domain.ProxyClass(req.Class), Template: req.Template, Limits: req.Limits.toDomain(),
		})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, toProxyEndpoint(view))
	return nil
}

func (a *api) deleteProxyEndpoint(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if err := a.svc.DeleteProxyEndpoint(ctx, actorFrom(ctx), r.PathValue("ref"), r.PathValue("name")); err != nil {
		return err
	}
	noContent(w)
	return nil
}
