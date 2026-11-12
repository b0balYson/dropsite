package client

import (
	"context"
	"net/http"
	"net/url"
)

type addHostnameRequest struct {
	Hostname string `json:"hostname"`
}

func hostnamesPath(ref string) string { return sitePath(ref) + "/hostnames" }

// ListHostnames returns every address of a site with its status.
func (c *Client) ListHostnames(ctx context.Context, ref string) (*List[Hostname], error) {
	var out List[Hostname]
	if err := c.doJSON(ctx, http.MethodGet, hostnamesPath(ref), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddHostname adds an external domain to a site; it waits for an administrator.
func (c *Client) AddHostname(ctx context.Context, ref, hostname string) (*Hostname, error) {
	var out Hostname
	if err := c.doJSON(ctx, http.MethodPost, hostnamesPath(ref), nil, addHostnameRequest{Hostname: hostname}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveHostname removes an external domain from a site.
func (c *Client) RemoveHostname(ctx context.Context, ref, hostname string) error {
	return c.doJSON(ctx, http.MethodDelete, hostnamesPath(ref)+"/"+url.PathEscape(hostname), nil, nil, nil)
}
