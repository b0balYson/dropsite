package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	// Class is "" to keep the class of an existing endpoint (a new one is ai).
	Class    string
	Template *ProxyTemplate
	Limits   ProxyLimits
}

// ProxyField is a value a visitor may send to an ai or send endpoint.
type ProxyField struct {
	Type      string `json:"type" jsonschema:"string, number, integer, boolean or messages (a chat history: a list of {role: user or assistant, content})"`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"longest text (or message) in characters; default 1000 (messages 4000), at most 20000"`
	MaxItems  int    `json:"max_items,omitempty" jsonschema:"most messages in a messages field; default 20"`
	Required  bool   `json:"required,omitempty"`
}

// ProxyTemplate is the fixed request of an ai or send endpoint.
type ProxyTemplate struct {
	Method string                `json:"method,omitempty" jsonschema:"POST (default), PUT or PATCH"`
	Path   string                `json:"path,omitempty" jsonschema:"path after upstream_url, e.g. /chat/completions or /sendMessage"`
	Body   any                   `json:"body" jsonschema:"the exact JSON body sent upstream, with the model, system prompt, max_tokens or the fixed recipient (chat_id); \"{{field}}\" marks where a visitor's value goes: a string that is exactly \"{{field}}\" becomes the typed value, inside longer text it is inserted as text"`
	Fields map[string]ProxyField `json:"fields" jsonschema:"the only values a visitor may send, by name (a-z, 0-9, _); every field must appear in body"`
	// Response is a dotted path into the JSON answer.
	Response string `json:"response,omitempty" jsonschema:"dotted path of the part of the answer the page gets as {\"result\": value}, e.g. choices.0.message.content; empty returns the whole answer"`
	Stream   bool   `json:"stream,omitempty" jsonschema:"true for a server-sent event stream (set stream true in body too); each event is reduced to the response path, e.g. choices.0.delta.content"`
}

// ProxyLimits are per-endpoint limits; 0 means the class default.
type ProxyLimits struct {
	PerMinute    int `json:"per_minute" jsonschema:"calls per minute per visitor IP"`
	DailyBudget  int `json:"daily_budget" jsonschema:"calls per UTC day for the whole endpoint (0: none)"`
	SessionQuota int `json:"session_quota" jsonschema:"calls per visitor session (ai and send)"`
}

// TemplateFromJSON decodes a template written by another package with the
// same JSON shape (domain or client).
func TemplateFromJSON(raw []byte) (*ProxyTemplate, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var t ProxyTemplate
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("decode proxy template: %w", err)
	}
	return &t, nil
}

// TemplateJSON encodes t for another package with the same JSON shape.
func TemplateJSON(t *ProxyTemplate) ([]byte, error) {
	if t == nil {
		return nil, nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("encode proxy template: %w", err)
	}
	return b, nil
}

// ProxyUsage are the usage counters of an endpoint.
type ProxyUsage struct {
	Requests   int64  `json:"requests" jsonschema:"calls forwarded to the upstream service"`
	Errors     int64  `json:"errors" jsonschema:"forwarded calls that failed (upstream 5xx or unreachable)"`
	Rejected   int64  `json:"rejected" jsonschema:"calls refused before forwarding (method, path, other site, too many, no key, too large, no session)"`
	LastUsedAt string `json:"last_used_at,omitempty" jsonschema:"UTC, RFC 3339"`
	LastStatus int    `json:"last_status,omitempty" jsonschema:"HTTP status of the last call"`
	Today      int    `json:"today" jsonschema:"calls of the current UTC day counted against the daily budget"`
	Paused     bool   `json:"paused" jsonschema:"the daily budget ran out: calls are refused until midnight UTC"`
}

// TransparentWarning is the warning of the transparent class.
const TransparentWarning = "transparent class: anyone who opens the site can call this API with the user's key " +
	"for any allowed method and path. Recommended only for password-protected sites; otherwise switch to class " +
	"ai (model APIs), send (messages to a fixed recipient) or read (GET-only data)."

// ProxyEndpoint is an endpoint without its key.
type ProxyEndpoint struct {
	Name            string         `json:"name"`
	UpstreamURL     string         `json:"upstream_url"`
	AllowedHosts    []string       `json:"allowed_hosts"`
	Methods         []string       `json:"methods"`
	Paths           []string       `json:"paths" jsonschema:"allowed path prefixes; empty allows every path"`
	Auth            string         `json:"auth" jsonschema:"header, query, bearer or path"`
	AuthName        string         `json:"auth_name,omitempty"`
	Enabled         bool           `json:"enabled"`
	SecretSet       bool           `json:"secret_set" jsonschema:"whether the user has pasted the key in the web interface"`
	SecretUpdatedAt string         `json:"secret_updated_at,omitempty"`
	SecretURL       string         `json:"secret_url" jsonschema:"web interface page where the user pastes the key; give it to the user as a link"`
	CallURL         string         `json:"call_url" jsonschema:"address the site's pages call, followed by the upstream path; in the site's code use the relative path"`
	Usage           ProxyUsage     `json:"usage"`
	Summary         string         `json:"summary" jsonschema:"the state in plain words, ready to tell the user"`
	Class           string         `json:"class" jsonschema:"ai, send, read or transparent"`
	Template        *ProxyTemplate `json:"template,omitempty" jsonschema:"request template of an ai or send endpoint"`
	Limits          ProxyLimits    `json:"limits" jsonschema:"limits set by the user; 0 means the class default"`
	EffectiveLimits ProxyLimits    `json:"effective_limits" jsonschema:"limits in force"`
	Warning         string         `json:"warning,omitempty" jsonschema:"tell the user when set"`
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
	if e.Template != nil {
		fmt.Fprintf(&b, "%s (class %s) sends its fixed template to %s%s", e.Name, e.Class, e.UpstreamURL, e.Template.Path)
	} else {
		fmt.Fprintf(&b, "%s (class %s) forwards %s calls to %s", e.Name, e.Class, strings.Join(e.Methods, ", "), e.UpstreamURL)
		if len(e.Paths) > 0 {
			fmt.Fprintf(&b, " (only %s)", strings.Join(e.Paths, ", "))
		}
	}
	b.WriteString(". ")
	if e.Warning != "" {
		b.WriteString("Warning: " + e.Warning + " ")
	}
	l := e.EffectiveLimits
	fmt.Fprintf(&b, "Limits: %d calls per minute per visitor", l.PerMinute)
	if l.DailyBudget > 0 {
		fmt.Fprintf(&b, ", %d per UTC day (%d used today)", l.DailyBudget, e.Usage.Today)
	}
	if l.SessionQuota > 0 {
		fmt.Fprintf(&b, ", %d per visitor session", l.SessionQuota)
	}
	b.WriteString(". ")
	if e.Usage.Paused {
		b.WriteString("The daily budget is used up: calls are refused until midnight UTC; the user can raise daily_budget. ")
	}
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
	if e.Template != nil {
		names := make([]string, 0, len(e.Template.Fields))
		for n := range e.Template.Fields {
			names = append(names, n)
		}
		sort.Strings(names)
		call = "In the site's pages add <script src=\"/_dropsite/session.js\"></script> and call " +
			"dropsite.fetch('/_dropsite/proxy/" + e.Name + "', {method: 'POST', headers: {'Content-Type': " +
			"'application/json'}, body: JSON.stringify({" + strings.Join(names, ", ") + "})}) — only these " +
			"fields, nothing else; dropsite.fetch passes the invisible anti-robot check by itself."
		if e.Template.Response != "" {
			call += " The answer is {\"result\": ...} with just the " + e.Template.Response + " value"
			if e.Template.Stream {
				call += " (a text/event-stream whose data lines are JSON strings, then [DONE])"
			}
			call += "."
		}
	}
	if e.Warning != "" {
		call += " " + e.Warning
	}
	if !e.SecretSet {
		return "Ask the user to open " + e.SecretURL + " and paste the API key there themselves. Do not ask " +
			"for the key in the chat and never put it in the site's files. " + call
	}
	return "The key is already stored and stays on the server. " + call
}

// NotFoundProxy is the not-found text of the proxy tools.
const NotFoundProxy = "not found: no such site or proxy endpoint among this user's sites; " +
	"call list_sites (or list_proxy_endpoints) to see valid IDs and names"
