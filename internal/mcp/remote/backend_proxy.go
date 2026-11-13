package remote

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

var _ mcp.ProxyBackend = backend{}

func (b backend) proxyError(err error) error {
	if err == nil {
		return nil
	}
	if e := domain.AsError(err); e.Kind == domain.KindNotFound {
		return errors.New(mcp.NotFoundProxy)
	}
	return b.toolError(err)
}

func toMCPLimits(l domain.ProxyLimits) mcp.ProxyLimits {
	return mcp.ProxyLimits{PerMinute: l.PerMinute, DailyBudget: l.DailyBudget, SessionQuota: l.SessionQuota}
}

func toProxyEndpoint(v service.ProxyEndpointView) mcp.ProxyEndpoint {
	e := v.Endpoint
	out := mcp.ProxyEndpoint{
		Name: e.Name, UpstreamURL: e.UpstreamURL, AllowedHosts: e.AllowedHosts, Methods: e.Methods, Paths: e.Paths,
		Auth: string(e.Auth), AuthName: e.AuthName, Enabled: e.Enabled, SecretSet: v.SecretSet,
		SecretUpdatedAt: stamp(e.SecretUpdatedAt), SecretURL: v.SecretURL, CallURL: v.CallURL,
		Usage: mcp.ProxyUsage{Requests: e.Usage.Requests, Errors: e.Usage.Errors, Rejected: e.Usage.Rejected,
			LastUsedAt: stamp(e.Usage.LastUsedAt), LastStatus: e.Usage.LastStatus, Today: e.Usage.Today,
			Paused: e.Usage.Paused},
		Class: string(e.Class), Limits: toMCPLimits(e.Limits), EffectiveLimits: toMCPLimits(v.Effective),
	}
	if e.Template != nil {
		if raw, err := json.Marshal(e.Template); err == nil {
			out.Template, _ = mcp.TemplateFromJSON(raw)
		}
	}
	return out
}

func toDomainTemplate(t *mcp.ProxyTemplate) (*domain.ProxyTemplate, error) {
	raw, err := mcp.TemplateJSON(t)
	if err != nil || raw == nil {
		return nil, err
	}
	var out domain.ProxyTemplate
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, domain.E(domain.KindInvalid, domain.CodeProxyInvalid, "template is not valid: "+err.Error())
	}
	return &out, nil
}

func (b backend) ListProxyEndpoints(ctx context.Context, ref string) ([]mcp.ProxyEndpoint, error) {
	eps, err := b.svc.ListProxyEndpoints(ctx, b.actor, ref)
	if err != nil {
		return nil, b.proxyError(err)
	}
	out := make([]mcp.ProxyEndpoint, 0, len(eps))
	for _, e := range eps {
		out = append(out, toProxyEndpoint(e))
	}
	return out, nil
}

func (b backend) SetProxyEndpoint(ctx context.Context, ref string, in mcp.ProxyEndpointInput) (mcp.ProxyEndpoint, bool, error) {
	tmpl, err := toDomainTemplate(in.Template)
	if err != nil {
		return mcp.ProxyEndpoint{}, false, b.proxyError(err)
	}
	v, created, err := b.svc.PutProxyEndpoint(ctx, b.actor, ref, in.Name, domain.ProxyConfig{
		UpstreamURL: in.UpstreamURL, AllowedHosts: in.AllowedHosts, Methods: in.Methods, Paths: in.Paths,
		Auth: domain.ProxyAuth(in.Auth), AuthName: in.AuthName, Enabled: in.Enabled,
		Class: domain.ProxyClass(in.Class), Template: tmpl,
		Limits: domain.ProxyLimits{PerMinute: in.Limits.PerMinute, DailyBudget: in.Limits.DailyBudget,
			SessionQuota: in.Limits.SessionQuota},
	})
	if err != nil {
		return mcp.ProxyEndpoint{}, false, b.proxyError(err)
	}
	return toProxyEndpoint(v), created, nil
}

func (b backend) DeleteProxyEndpoint(ctx context.Context, ref, name string) error {
	return b.proxyError(b.svc.DeleteProxyEndpoint(ctx, b.actor, ref, name))
}
