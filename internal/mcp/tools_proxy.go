package mcp

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Secret proxy tools, registered with the cabinet tools; the backend must
// implement ProxyBackend. No tool takes or returns a key.

type proxySetArgs struct {
	Site         string         `json:"site" jsonschema:"site ID or slug"`
	Name         string         `json:"name" jsonschema:"endpoint name, a-z, 0-9 and hyphens, e.g. openai or crm"`
	UpstreamURL  string         `json:"upstream_url" jsonschema:"https base address of the API, e.g. https://api.openai.com/v1; for Telegram https://api.telegram.org/bot{secret}; never put a key in it"`
	Auth         string         `json:"auth" jsonschema:"how the key is added: bearer (Authorization: Bearer <key>), header (header auth_name), query (query parameter auth_name) or path ({secret} in upstream_url)"`
	AuthName     string         `json:"auth_name,omitempty" jsonschema:"header name such as X-Api-Key (auth header) or query parameter such as api_key (auth query)"`
	Methods      []string       `json:"methods,omitempty" jsonschema:"allowed methods, e.g. [POST]; default GET and POST"`
	Paths        []string       `json:"paths,omitempty" jsonschema:"allowed path prefixes after the base address, e.g. [/chat/completions]; keep them as narrow as the page needs; default every path"`
	AllowedHosts []string       `json:"allowed_hosts,omitempty" jsonschema:"extra exact https hosts the API may redirect to; the upstream host is always allowed"`
	Enabled      *bool          `json:"enabled,omitempty" jsonschema:"false turns the endpoint off; default true"`
	Class        string         `json:"class,omitempty" jsonschema:"ai: a model API (OpenAI, Anthropic, DeepSeek...) called only with your template; send: a message to a recipient fixed in the template (Telegram bot, CRM lead); read: GET-only data (maps, weather); transparent: any allowed call, only for password-protected sites and only if the user explicitly wants it. Omitted: a new endpoint is ai, an existing one keeps its class"`
	Template     *ProxyTemplate `json:"template,omitempty" jsonschema:"required for ai and send: the fixed request; the visitor sends only the declared fields"`
	PerMinute    int            `json:"per_minute,omitempty" jsonschema:"calls per minute per visitor IP; 0 keeps the class default (ai 20, send 5, read and transparent 60)"`
	DailyBudget  int            `json:"daily_budget,omitempty" jsonschema:"calls per UTC day for the whole endpoint, after which it pauses until midnight UTC and the user is notified; 0 keeps the class default (ai 1000, send 100, read 10000, transparent none)"`
	SessionQuota int            `json:"session_quota,omitempty" jsonschema:"calls per visitor session for ai and send; 0 keeps the default 50"`
}

type proxyDeleteArgs struct {
	Site    string `json:"site" jsonschema:"site ID or slug"`
	Name    string `json:"name" jsonschema:"endpoint name from list_proxy_endpoints"`
	Confirm string `json:"confirm" jsonschema:"the endpoint name typed again after the user explicitly agreed"`
}

type proxyDeleted struct {
	Deleted string `json:"deleted" jsonschema:"name of the deleted endpoint"`
}

// ErrProxyConfirm is returned by delete_proxy_endpoint when confirm does not
// name the endpoint.
var ErrProxyConfirm = errors.New("nothing deleted: ask the user to confirm, then call again with confirm set " +
	"to the endpoint name exactly as shown by list_proxy_endpoints")

var errProxyName = errors.New("name is required: a-z, 0-9 and hyphens, e.g. openai")

var errNoProxy = errors.New("this server cannot manage API keys; use the web interface of Dropsite")

func (t tools) registerProxyTools(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_proxy_endpoints", Annotations: readOnly("List API key proxies"),
		Description: "List a site's secret proxy endpoints: which API each forwards to, whether the user has " +
			"pasted the key (secret_set), the link where the key is pasted, the address pages call and usage " +
			"counters. Keys are never shown. Use it before changing an endpoint or when calls from the site fail."},
		t.listProxyEndpoints)
	sdk.AddTool(s, &sdk.Tool{Name: "set_proxy_endpoint", Annotations: writes("Set up API key proxy", false, true),
		Description: "Create or replace a secret proxy endpoint when a page must call a third-party API with a " +
			"secret key (\"add an AI chat to the site\": OpenAI or another model API, a CRM, a Telegram bot, maps): " +
			"Dropsite adds the key on the server. Choose the class: ai for model APIs and send for messages take a " +
			"template (the fixed body with {{field}} places and the fields a visitor may send), so nobody can use " +
			"the key for anything else; the page loads /_dropsite/session.js and calls dropsite.fetch. read is for " +
			"GET-only data; transparent forwards anything and is only for password-protected sites. Pass the whole " +
			"configuration each time. There is no key parameter: the user pastes the key in the web interface at " +
			"secret_url; never ask for it in the chat. Changing upstream_url, allowed_hosts, auth or auth_name " +
			"removes a stored key, so the user has to paste it again. Follow next_step. Not for contact forms " +
			"(use the site's forms) or sign-in and databases with a public key such as Supabase (get_guide topic " +
			"external_backend); details: get_guide topic proxy."}, t.setProxyEndpoint)
	sdk.AddTool(s, &sdk.Tool{Name: "delete_proxy_endpoint", Annotations: writes("Delete API key proxy", true, true),
		Description: "Delete a secret proxy endpoint and its stored key; pages calling it stop working. Only " +
			"after the user explicitly confirmed: pass the endpoint name again in confirm."}, t.deleteProxyEndpoint)
}

func (t tools) proxy(ctx context.Context, req *sdk.CallToolRequest) (ProxyBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	p, ok := b.(ProxyBackend)
	if !ok {
		return nil, errNoProxy
	}
	return p, nil
}

func withProxySummary(e ProxyEndpoint) ProxyEndpoint {
	if e.AllowedHosts == nil {
		e.AllowedHosts = []string{}
	}
	if e.Methods == nil {
		e.Methods = []string{}
	}
	if e.Paths == nil {
		e.Paths = []string{}
	}
	e.Warning = ""
	if e.Class == "transparent" {
		e.Warning = TransparentWarning
	}
	e.Summary = ProxySummary(e)
	return e
}

func (t tools) listProxyEndpoints(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, ProxyEndpointList, error) {
	b, err := t.proxy(ctx, req)
	if err != nil {
		return nil, ProxyEndpointList{}, err
	}
	eps, err := b.ListProxyEndpoints(ctx, in.Site)
	if err != nil {
		return nil, ProxyEndpointList{}, err
	}
	out := ProxyEndpointList{Endpoints: make([]ProxyEndpoint, 0, len(eps))}
	for _, e := range eps {
		out.Endpoints = append(out.Endpoints, withProxySummary(e))
	}
	return nil, out, nil
}

func (t tools) setProxyEndpoint(ctx context.Context, req *sdk.CallToolRequest, in proxySetArgs) (*sdk.CallToolResult, ProxyEndpointSaved, error) {
	b, err := t.proxy(ctx, req)
	if err != nil {
		return nil, ProxyEndpointSaved{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, ProxyEndpointSaved{}, errProxyName
	}
	enabled := in.Enabled == nil || *in.Enabled
	e, created, err := b.SetProxyEndpoint(ctx, in.Site, ProxyEndpointInput{
		Name: strings.TrimSpace(in.Name), UpstreamURL: in.UpstreamURL, AllowedHosts: in.AllowedHosts,
		Methods: in.Methods, Paths: in.Paths, Auth: in.Auth, AuthName: in.AuthName, Enabled: enabled,
		Class: strings.TrimSpace(in.Class), Template: in.Template,
		Limits: ProxyLimits{PerMinute: in.PerMinute, DailyBudget: in.DailyBudget, SessionQuota: in.SessionQuota},
	})
	if err != nil {
		return nil, ProxyEndpointSaved{}, err
	}
	e = withProxySummary(e)
	return nil, ProxyEndpointSaved{Endpoint: e, Created: created, NextStep: ProxyNextStep(e)}, nil
}

func (t tools) deleteProxyEndpoint(ctx context.Context, req *sdk.CallToolRequest, in proxyDeleteArgs) (*sdk.CallToolResult, proxyDeleted, error) {
	b, err := t.proxy(ctx, req)
	if err != nil {
		return nil, proxyDeleted{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || strings.TrimSpace(in.Confirm) != name {
		return nil, proxyDeleted{}, ErrProxyConfirm
	}
	if err := b.DeleteProxyEndpoint(ctx, in.Site, name); err != nil {
		return nil, proxyDeleted{}, err
	}
	return nil, proxyDeleted{Deleted: name}, nil
}
