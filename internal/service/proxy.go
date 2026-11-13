package service

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
)

// Secret proxy call limits (per site address).
const (
	ProxyCallsPerIP       = 60
	ProxyCallsPerIPWindow = time.Minute
	ProxyCallsPerSite     = 1000
	ProxyCallsSiteWindow  = time.Hour
	proxySecretCtx        = "proxy-endpoint:"
	// ProxyPathPrefix is where a site page calls its endpoints.
	ProxyPathPrefix = "/_dropsite/proxy/"
)

// proxyState is the secret proxy part of Service, created in New.
type proxyState struct {
	proxyClient    *http.Client
	proxyIPLimit   *ratelimit.Window
	proxySiteLimit *ratelimit.Window
	proxyRejected  *rejectedUsage
}

func newProxyState(d Deps, now func() time.Time) proxyState {
	tr := d.ProxyTransport
	if tr == nil {
		tr = newProxyTransport(selfPrefixes())
	}
	return proxyState{
		proxyClient:    newProxyClient(tr),
		proxyIPLimit:   ratelimit.New(ProxyCallsPerIP, ProxyCallsPerIPWindow).WithClock(now),
		proxySiteLimit: ratelimit.New(ProxyCallsPerSite, ProxyCallsSiteWindow).WithClock(now),
		proxyRejected:  newRejectedUsage(),
	}
}

// ProxyEndpointView is an endpoint as shown to its owner: never the secret.
type ProxyEndpointView struct {
	Endpoint  domain.ProxyEndpoint // SecretEnc is always empty
	SecretSet bool
	// CallURL is the address a page of the site calls (with a trailing /).
	CallURL string
	// SecretURL is the web interface screen where the key is pasted.
	SecretURL string
}

var errProxyNotUI = domain.E(domain.KindInvalid, domain.CodeProxySecretUIOnly,
	"the key of a proxy endpoint is entered only in the web interface")

// ProxyPageURL is the web interface screen with the site's endpoints; with
// a name it points at that endpoint's key form.
func (s *Service) ProxyPageURL(siteID, name string) string {
	u := s.cabinetBase() + "/sites/" + siteID + "/proxy"
	if name != "" {
		u += "#proxy-" + name
	}
	return u
}

// ErrProxySecretUIOnly is the API answer to a request carrying a secret.
func (s *Service) ErrProxySecretUIOnly(siteID, name string) error {
	return errProxyNotUI.WithParams(map[string]any{"secret_url": s.ProxyPageURL(siteID, name)})
}

func (s *Service) proxyView(siteURL string, e domain.ProxyEndpoint) ProxyEndpointView {
	set := e.SecretSet()
	e.SecretEnc = ""
	call := ""
	if siteURL != "" {
		call = strings.TrimSuffix(siteURL, "/") + ProxyPathPrefix + e.Name + "/"
	}
	return ProxyEndpointView{Endpoint: e, SecretSet: set, CallURL: call, SecretURL: s.ProxyPageURL(e.SiteID, e.Name)}
}

func (s *Service) siteURL(ctx context.Context, site domain.Site) (string, error) {
	v, err := s.view(ctx, site)
	if err != nil {
		return "", err
	}
	return v.URL, nil
}

// ListProxyEndpoints returns the site's endpoints without secrets.
func (s *Service) ListProxyEndpoints(ctx context.Context, a Actor, ref string) ([]ProxyEndpointView, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	base, err := s.siteURL(ctx, site)
	if err != nil {
		return nil, err
	}
	s.flushProxyRejected(ctx)
	eps, err := s.repo.ListProxyEndpoints(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	out := make([]ProxyEndpointView, 0, len(eps))
	for _, e := range eps {
		out = append(out, s.proxyView(base, e))
	}
	return out, nil
}

// GetProxyEndpoint returns one endpoint of the site without its secret.
func (s *Service) GetProxyEndpoint(ctx context.Context, a Actor, ref, name string) (ProxyEndpointView, error) {
	site, e, err := s.proxyEndpointOf(ctx, a, ref, name)
	if err != nil {
		return ProxyEndpointView{}, err
	}
	base, err := s.siteURL(ctx, site)
	if err != nil {
		return ProxyEndpointView{}, err
	}
	return s.proxyView(base, e), nil
}

func (s *Service) proxyEndpointOf(ctx context.Context, a Actor, ref, name string) (domain.Site, domain.ProxyEndpoint, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return domain.Site{}, domain.ProxyEndpoint{}, err
	}
	n, err := normalizeProxyName(name)
	if err != nil {
		return domain.Site{}, domain.ProxyEndpoint{}, domain.ErrNotFound
	}
	s.flushProxyRejected(ctx)
	e, err := s.repo.GetProxyEndpoint(ctx, site.ID, n)
	if err != nil {
		return domain.Site{}, domain.ProxyEndpoint{}, err
	}
	return site, e, nil
}

// PutProxyEndpoint creates the endpoint or replaces its configuration. A
// stored secret survives only when the destination (upstream URL, hosts,
// placement) is unchanged, so a changed configuration can never send the
// key somewhere else. created tells whether the endpoint is new.
func (s *Service) PutProxyEndpoint(ctx context.Context, a Actor, ref, name string, c domain.ProxyConfig) (ProxyEndpointView, bool, error) {
	site, err := s.resolveSite(ctx, a, ref)
	if err != nil {
		return ProxyEndpointView{}, false, err
	}
	n, err := normalizeProxyName(name)
	if err != nil {
		return ProxyEndpointView{}, false, err
	}
	cfg, err := s.normalizeProxyConfig(c)
	if err != nil {
		return ProxyEndpointView{}, false, err
	}
	base, err := s.siteURL(ctx, site)
	if err != nil {
		return ProxyEndpointView{}, false, err
	}
	now := s.now()
	old, err := s.repo.GetProxyEndpoint(ctx, site.ID, n)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		e, err := s.createProxyEndpoint(ctx, a, site, n, cfg, now)
		if err != nil {
			return ProxyEndpointView{}, false, err
		}
		return s.proxyView(base, e), true, nil
	case err != nil:
		return ProxyEndpointView{}, false, err
	}
	e := old
	e.ProxyConfig, e.UpdatedAt = cfg, now
	cleared := old.SecretSet() && proxyDestinationChanged(old.ProxyConfig, cfg)
	if cleared {
		e.SecretEnc, e.SecretUpdatedAt = "", &now
	}
	if err := s.repo.UpdateProxyEndpoint(ctx, e); err != nil {
		return ProxyEndpointView{}, false, err
	}
	s.audit(ctx, a, "proxy.endpoint.update", siteTarget(site.ID), map[string]any{
		"name": n, "host": cfg.AllowedHosts[0], "enabled": cfg.Enabled, "secret_cleared": cleared,
	})
	return s.proxyView(base, e), false, nil
}

func (s *Service) createProxyEndpoint(ctx context.Context, a Actor, site domain.Site, name string, cfg domain.ProxyConfig, now time.Time) (domain.ProxyEndpoint, error) {
	count, err := s.repo.CountProxyEndpoints(ctx, site.ID)
	if err != nil {
		return domain.ProxyEndpoint{}, err
	}
	if count >= ProxyEndpointsPerSite {
		return domain.ProxyEndpoint{}, domain.E(domain.KindLimit, domain.CodeProxyLimit,
			"a site can have at most "+strconv.Itoa(ProxyEndpointsPerSite)+" proxy endpoints").
			WithParams(map[string]any{"limit": ProxyEndpointsPerSite})
	}
	e := domain.ProxyEndpoint{SiteID: site.ID, Name: name, ProxyConfig: cfg, CreatedAt: now, UpdatedAt: now}
	if e.ID, err = s.repo.CreateProxyEndpoint(ctx, e); err != nil {
		return domain.ProxyEndpoint{}, err
	}
	s.audit(ctx, a, "proxy.endpoint.create", siteTarget(site.ID), map[string]any{
		"name": name, "host": cfg.AllowedHosts[0], "auth": string(cfg.Auth),
	})
	return e, nil
}

// DeleteProxyEndpoint removes an endpoint together with its secret.
func (s *Service) DeleteProxyEndpoint(ctx context.Context, a Actor, ref, name string) error {
	site, e, err := s.proxyEndpointOf(ctx, a, ref, name)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteProxyEndpoint(ctx, site.ID, e.ID); err != nil {
		return err
	}
	s.audit(ctx, a, "proxy.endpoint.delete", siteTarget(site.ID), map[string]any{
		"name": e.Name, "host": firstHost(e), "had_secret": e.SecretSet(),
	})
	return nil
}

func firstHost(e domain.ProxyEndpoint) string {
	if len(e.AllowedHosts) == 0 {
		return ""
	}
	return e.AllowedHosts[0]
}

// SetProxySecret stores (or replaces) the key of an endpoint. Only the web
// interface calls it; an actor working for an AI assistant is refused.
func (s *Service) SetProxySecret(ctx context.Context, a Actor, ref, name, secret string) (ProxyEndpointView, error) {
	if a.ViaMCP {
		return ProxyEndpointView{}, errProxyNotUI
	}
	site, e, err := s.proxyEndpointOf(ctx, a, ref, name)
	if err != nil {
		return ProxyEndpointView{}, err
	}
	secret, err = normalizeProxySecret(secret)
	if err != nil {
		return ProxyEndpointView{}, err
	}
	now := s.now()
	replaced := e.SecretSet()
	e.SecretEnc, e.SecretUpdatedAt, e.UpdatedAt = s.sealer.Seal(secret, proxySealContext(e)), &now, now
	if err := s.repo.UpdateProxyEndpoint(ctx, e); err != nil {
		return ProxyEndpointView{}, err
	}
	s.audit(ctx, a, "proxy.secret.set", siteTarget(site.ID), map[string]any{
		"name": e.Name, "host": firstHost(e), "replaced": replaced,
	})
	base, err := s.siteURL(ctx, site)
	if err != nil {
		return ProxyEndpointView{}, err
	}
	return s.proxyView(base, e), nil
}

// ClearProxySecret removes the key of an endpoint; calls then answer
// proxy_secret_missing until a new key is pasted.
func (s *Service) ClearProxySecret(ctx context.Context, a Actor, ref, name string) error {
	site, e, err := s.proxyEndpointOf(ctx, a, ref, name)
	if err != nil {
		return err
	}
	if !e.SecretSet() {
		return nil
	}
	now := s.now()
	e.SecretEnc, e.SecretUpdatedAt, e.UpdatedAt = "", &now, now
	if err := s.repo.UpdateProxyEndpoint(ctx, e); err != nil {
		return err
	}
	s.audit(ctx, a, "proxy.secret.clear", siteTarget(site.ID), map[string]any{"name": e.Name, "host": firstHost(e)})
	return nil
}

// proxySealContext binds a sealed secret to one endpoint row.
func proxySealContext(e domain.ProxyEndpoint) string {
	return proxySecretCtx + e.SiteID + ":" + strconv.FormatInt(e.ID, 10)
}

// purgeProxy deletes the endpoints of deleted sites (called by Housekeeping).
func (s *Service) purgeProxy(ctx context.Context) error {
	if _, err := s.repo.PurgeProxyEndpoints(ctx); err != nil {
		return err
	}
	return nil
}
