package remote

import (
	"context"
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

func toProxyEndpoint(v service.ProxyEndpointView) mcp.ProxyEndpoint {
	e := v.Endpoint
	return mcp.ProxyEndpoint{
		Name: e.Name, UpstreamURL: e.UpstreamURL, AllowedHosts: e.AllowedHosts, Methods: e.Methods, Paths: e.Paths,
		Auth: string(e.Auth), AuthName: e.AuthName, Enabled: e.Enabled, SecretSet: v.SecretSet,
		SecretUpdatedAt: stamp(e.SecretUpdatedAt), SecretURL: v.SecretURL, CallURL: v.CallURL,
		Usage: mcp.ProxyUsage{Requests: e.Usage.Requests, Errors: e.Usage.Errors, Rejected: e.Usage.Rejected,
			LastUsedAt: stamp(e.Usage.LastUsedAt), LastStatus: e.Usage.LastStatus},
	}
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
	v, created, err := b.svc.PutProxyEndpoint(ctx, b.actor, ref, in.Name, domain.ProxyConfig{
		UpstreamURL: in.UpstreamURL, AllowedHosts: in.AllowedHosts, Methods: in.Methods, Paths: in.Paths,
		Auth: domain.ProxyAuth(in.Auth), AuthName: in.AuthName, Enabled: in.Enabled,
	})
	if err != nil {
		return mcp.ProxyEndpoint{}, false, b.proxyError(err)
	}
	return toProxyEndpoint(v), created, nil
}

func (b backend) DeleteProxyEndpoint(ctx context.Context, ref, name string) error {
	return b.proxyError(b.svc.DeleteProxyEndpoint(ctx, b.actor, ref, name))
}
