package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ProxyUsage are the usage counters of a secret proxy endpoint.
type ProxyUsage struct {
	Requests   int64      `json:"requests"`
	Errors     int64      `json:"errors"`
	Rejected   int64      `json:"rejected"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastStatus *int       `json:"last_status"`
}

// ProxyEndpoint is a secret proxy endpoint of a site; the key is never
// returned by the API.
type ProxyEndpoint struct {
	Name            string     `json:"name"`
	UpstreamURL     string     `json:"upstream_url"`
	AllowedHosts    []string   `json:"allowed_hosts"`
	Methods         []string   `json:"methods"`
	Paths           []string   `json:"paths"`
	Auth            string     `json:"auth"`
	AuthName        *string    `json:"auth_name"`
	Enabled         bool       `json:"enabled"`
	SecretSet       bool       `json:"secret_set"`
	SecretUpdatedAt *time.Time `json:"secret_updated_at"`
	SecretURL       string     `json:"secret_url"`
	CallURL         string     `json:"call_url"`
	Usage           ProxyUsage `json:"usage"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ProxyEndpointConfig is the body of PUT .../proxy/endpoints/{name}. It has
// no key field: keys are pasted in the web interface.
type ProxyEndpointConfig struct {
	UpstreamURL  string   `json:"upstream_url"`
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
	Methods      []string `json:"methods,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	Auth         string   `json:"auth"`
	AuthName     string   `json:"auth_name,omitempty"`
	Enabled      bool     `json:"enabled"`
}

func proxyPath(ref string) string { return "/sites/" + url.PathEscape(ref) + "/proxy/endpoints" }

// ListProxyEndpoints returns the site's secret proxy endpoints.
func (c *Client) ListProxyEndpoints(ctx context.Context, ref string) (*List[ProxyEndpoint], error) {
	var out List[ProxyEndpoint]
	if err := c.doJSON(ctx, http.MethodGet, proxyPath(ref), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutProxyEndpoint creates the endpoint or replaces its configuration;
// created reports a new endpoint (201).
func (c *Client) PutProxyEndpoint(ctx context.Context, ref, name string, cfg ProxyEndpointConfig) (*ProxyEndpoint, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("encode request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPut, proxyPath(ref)+"/"+url.PathEscape(name), nil, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, c.networkError(req, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, false, parseAPIError(resp)
	}
	var out ProxyEndpoint
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return nil, false, fmt.Errorf("%s %s: decode response: %w", req.Method, req.URL.Path, err)
	}
	return &out, resp.StatusCode == http.StatusCreated, nil
}

// DeleteProxyEndpoint deletes an endpoint and its key.
func (c *Client) DeleteProxyEndpoint(ctx context.Context, ref, name string) error {
	return c.doJSON(ctx, http.MethodDelete, proxyPath(ref)+"/"+url.PathEscape(name), nil, nil, nil)
}
