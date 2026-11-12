package mcp

import (
	"context"
	"fmt"
	"strings"
)

// ProxyBackend manages the secret proxy endpoints of the user's sites. It
// never handles the keys: they are pasted in the web interface.
type ProxyBackend interface {
	ListProxyEndpoints(ctx context.Context, ref string) ([]ProxyEndpoint, error)
	SetProxyEndpoint(ctx context.Context, ref string, in ProxyEndpointInput) (ProxyEndpoint, bool, error)
	DeleteProxyEndpoint(ctx context.Context, ref, name string) error
}

// ProxyEndpointInput is the configuration set by set_proxy_endpoint.
type ProxyEndpointInput struct {
	Name         string
	UpstreamURL  string
	AllowedHosts []string
	Methods      []string
	Paths        []string
	Auth         string
	AuthName     string
	Enabled      bool
}

// ProxyUsage are the usage counters of an endpoint.
type ProxyUsage struct {
	Requests   int64  `json:"requests" jsonschema:"calls forwarded to the upstream service"`
	Errors     int64  `json:"errors" jsonschema:"forwarded calls that failed (upstream 5xx or unreachable)"`
	Rejected   int64  `json:"rejected" jsonschema:"calls refused before forwarding (method, path, other site, too many, no key, too large)"`
	LastUsedAt string `json:"last_used_at,omitempty" jsonschema:"UTC, RFC 3339"`
	LastStatus int    `json:"last_status,omitempty" jsonschema:"HTTP status of the last call"`
}

// ProxyEndpoint is an endpoint without its key.
type ProxyEndpoint struct {
	Name            string     `json:"name"`
	UpstreamURL     string     `json:"upstream_url"`
	AllowedHosts    []string   `json:"allowed_hosts"`
	Methods         []string   `json:"methods"`
	Paths           []string   `json:"paths" jsonschema:"allowed path prefixes; empty allows every path"`
	Auth            string     `json:"auth" jsonschema:"header, query, bearer or path"`
	AuthName        string     `json:"auth_name,omitempty"`
	Enabled         bool       `json:"enabled"`
	SecretSet       bool       `json:"secret_set" jsonschema:"whether the user has pasted the key in the web interface"`
	SecretUpdatedAt string     `json:"secret_updated_at,omitempty"`
	SecretURL       string     `json:"secret_url" jsonschema:"web interface page where the user pastes the key; give it to the user as a link"`
	CallURL         string     `json:"call_url" jsonschema:"address the site's pages call, followed by the upstream path; in the site's code use the relative path"`
	Usage           ProxyUsage `json:"usage"`
	Summary         string     `json:"summary" jsonschema:"the state in plain words, ready to tell the user"`
}

// ProxyEndpointList is the output of list_proxy_endpoints.
type ProxyEndpointList struct {
	Endpoints []ProxyEndpoint `json:"endpoints"`
}

// ProxyEndpointSaved is the output of set_proxy_endpoint.
type ProxyEndpointSaved struct {
	Endpoint ProxyEndpoint `json:"endpoint"`
	Created  bool          `json:"created"`
	NextStep string        `json:"next_step" jsonschema:"what to tell the user and do next"`
}

// RelativeProxyPath is the path a page of the site uses to call an endpoint.
func RelativeProxyPath(name string) string { return "/_dropsite/proxy/" + name + "/" }

// ProxySummary describes an endpoint in plain words.
func ProxySummary(e ProxyEndpoint) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s forwards %s calls to %s", e.Name, strings.Join(e.Methods, ", "), e.UpstreamURL)
	if len(e.Paths) > 0 {
		fmt.Fprintf(&b, " (only %s)", strings.Join(e.Paths, ", "))
	}
	b.WriteString(". ")
	switch {
	case !e.Enabled:
		b.WriteString("It is turned off. ")
	case !e.SecretSet:
		b.WriteString("The key is not set yet, so calls are refused until the user pastes it at " + e.SecretURL + ". ")
	default:
		b.WriteString("The key is set")
		if e.SecretUpdatedAt != "" {
			b.WriteString(" (" + e.SecretUpdatedAt + ")")
		}
		b.WriteString(". ")
	}
	u := e.Usage
	fmt.Fprintf(&b, "%d calls forwarded, %d failed, %d refused", u.Requests, u.Errors, u.Rejected)
	if u.LastUsedAt != "" {
		fmt.Fprintf(&b, "; last call %s with status %d", u.LastUsedAt, u.LastStatus)
	}
	b.WriteString(".")
	return b.String()
}

// ProxyNextStep tells the model what to do after set_proxy_endpoint.
func ProxyNextStep(e ProxyEndpoint) string {
	call := "In the site's code call fetch('" + RelativeProxyPath(e.Name) + "<path>') with the same body and " +
		"headers the upstream API expects, but without any key."
	if !e.SecretSet {
		return "Ask the user to open " + e.SecretURL + " and paste the API key there themselves. Do not ask " +
			"for the key in the chat and never put it in the site's files. " + call
	}
	return "The key is already stored and stays on the server. " + call
}

// NotFoundProxy is the not-found text of the proxy tools.
const NotFoundProxy = "not found: no such site or proxy endpoint among this user's sites; " +
	"call list_sites (or list_proxy_endpoints) to see valid IDs and names"
