package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

// Secret proxy tools of the local server, through the API.

var _ mcp.ProxyBackend = localBackend{}

func proxyError(err error) error { return toolErrorFor(err, mcp.NotFoundProxy) }

func fromProxyEndpoint(e client.ProxyEndpoint) mcp.ProxyEndpoint {
	out := mcp.ProxyEndpoint{
		Name: e.Name, UpstreamURL: e.UpstreamURL, AllowedHosts: e.AllowedHosts, Methods: e.Methods, Paths: e.Paths,
		Auth: e.Auth, AuthName: deref(e.AuthName), Enabled: e.Enabled, SecretSet: e.SecretSet,
		SecretUpdatedAt: stampTime(e.SecretUpdatedAt), SecretURL: e.SecretURL, CallURL: e.CallURL,
		Usage: mcp.ProxyUsage{Requests: e.Usage.Requests, Errors: e.Usage.Errors, Rejected: e.Usage.Rejected,
			LastUsedAt: stampTime(e.Usage.LastUsedAt)},
	}
	if e.Usage.LastStatus != nil {
		out.Usage.LastStatus = *e.Usage.LastStatus
	}
	return out
}

func (b localBackend) ListProxyEndpoints(ctx context.Context, ref string) ([]mcp.ProxyEndpoint, error) {
	l, err := b.c.ListProxyEndpoints(ctx, ref)
	if err != nil {
		return nil, proxyError(err)
	}
	out := make([]mcp.ProxyEndpoint, 0, len(l.Items))
	for _, e := range l.Items {
		out = append(out, fromProxyEndpoint(e))
	}
	return out, nil
}

func (b localBackend) SetProxyEndpoint(ctx context.Context, ref string, in mcp.ProxyEndpointInput) (mcp.ProxyEndpoint, bool, error) {
	e, created, err := b.c.PutProxyEndpoint(ctx, ref, in.Name, client.ProxyEndpointConfig{
		UpstreamURL: in.UpstreamURL, AllowedHosts: in.AllowedHosts, Methods: in.Methods, Paths: in.Paths,
		Auth: in.Auth, AuthName: in.AuthName, Enabled: in.Enabled,
	})
	if err != nil {
		return mcp.ProxyEndpoint{}, false, proxyError(err)
	}
	return fromProxyEndpoint(*e), created, nil
}

func (b localBackend) DeleteProxyEndpoint(ctx context.Context, ref, name string) error {
	return proxyError(b.c.DeleteProxyEndpoint(ctx, ref, name))
}
