package domain

import (
	"encoding/json"
	"time"
)

// Secret proxy error codes (change secret-proxy). Management errors are
// returned by the API, MCP and UI; the visitor codes answer calls to
// /_dropsite/proxy/ on a site address.
const (
	CodeProxyInvalid      = "proxy_invalid"
	CodeProxyLimit        = "proxy_limit_reached"
	CodeProxySecretUIOnly = "proxy_secret_ui_only"
	CodeProxySecret       = "proxy_secret_invalid"

	CodeProxyOrigin        = "proxy_origin"
	CodeProxyMethod        = "proxy_method"
	CodeProxyPath          = "proxy_path"
	CodeProxySecretMissing = "proxy_secret_missing"
	CodeProxyTooLarge      = "proxy_too_large"
	CodeProxyUpstream      = "proxy_upstream"
	CodeProxyTimeout       = "proxy_timeout"
	// Abuse protection (change abuse-protection).
	CodeProxySession = "proxy_session"
	CodeProxyPow     = "proxy_pow"
	CodeProxyField   = "proxy_field"
	CodeProxyBudget  = "proxy_budget"
	CodeProxyStatus  = "proxy_upstream_status"
)

// ProxyClass is the kind of an endpoint; each class has built-in policies.
type ProxyClass string

const (
	// ProxyClassRead allows GET and HEAD only, without a visitor session.
	ProxyClassRead ProxyClass = "read"
	// ProxyClassAI forwards only its request template and needs a session.
	ProxyClassAI ProxyClass = "ai"
	// ProxyClassSend forwards only its template, with a session and a fresh
	// proof of work per call.
	ProxyClassSend ProxyClass = "send"
	// ProxyClassTransparent forwards any allowed call (the original proxy).
	ProxyClassTransparent ProxyClass = "transparent"
)

// ProxyClasses lists the classes in display order.
var ProxyClasses = []ProxyClass{ProxyClassAI, ProxyClassSend, ProxyClassRead, ProxyClassTransparent}

// Valid reports whether c is a known class.
func (c ProxyClass) Valid() bool {
	switch c {
	case ProxyClassRead, ProxyClassAI, ProxyClassSend, ProxyClassTransparent:
		return true
	}
	return false
}

// Templated reports whether the class forwards only its request template
// and needs a visitor session.
func (c ProxyClass) Templated() bool { return c == ProxyClassAI || c == ProxyClassSend }

// ProxyLimits are owner overrides of the class policy; 0 keeps the default.
type ProxyLimits struct {
	PerMinute    int // calls per minute and client network
	DailyBudget  int // calls per UTC day for the whole endpoint
	SessionQuota int // calls per visitor session
}

// ProxyField is a value a visitor may send to a templated endpoint.
type ProxyField struct {
	// Type is string, number, integer, boolean or messages.
	Type string `json:"type"`
	// MaxLength bounds a string (or each message content); MaxItems the
	// number of messages.
	MaxLength int  `json:"max_length,omitempty"`
	MaxItems  int  `json:"max_items,omitempty"`
	Required  bool `json:"required,omitempty"`
}

// Field types of a request template.
const (
	ProxyFieldString   = "string"
	ProxyFieldNumber   = "number"
	ProxyFieldInteger  = "integer"
	ProxyFieldBoolean  = "boolean"
	ProxyFieldMessages = "messages"
)

// ProxyTemplate is the declarative request of a templated endpoint: the
// server sends exactly Body to Path with Method, after replacing the
// "{{field}}" placeholders with the visitor's values. There are no
// expressions: placeholders are the only variable part.
type ProxyTemplate struct {
	Method string                `json:"method,omitempty"`
	Path   string                `json:"path,omitempty"`
	Body   json.RawMessage       `json:"body,omitempty"`
	Fields map[string]ProxyField `json:"fields,omitempty"`
	// Response is a dotted path into the JSON answer
	// (choices.0.message.content); empty returns the upstream answer as is.
	Response string `json:"response,omitempty"`
	// Stream forwards a server-sent event stream, each event reduced to
	// the Response value when Response is set.
	Stream bool `json:"stream,omitempty"`
}

// ProxyAuth is how the stored secret is added to a forwarded request.
type ProxyAuth string

const (
	// ProxyAuthHeader sets the header named AuthName to the secret.
	ProxyAuthHeader ProxyAuth = "header"
	// ProxyAuthQuery sets the query parameter named AuthName to the secret.
	ProxyAuthQuery ProxyAuth = "query"
	// ProxyAuthBearer sets "Authorization: Bearer <secret>".
	ProxyAuthBearer ProxyAuth = "bearer"
	// ProxyAuthPath replaces the {secret} mark in the upstream URL path.
	ProxyAuthPath ProxyAuth = "path"
)

// ProxySecretMark is the placeholder of the secret in an upstream URL with
// ProxyAuthPath.
const ProxySecretMark = "{secret}"

// Valid reports whether a is a known placement.
func (a ProxyAuth) Valid() bool {
	switch a {
	case ProxyAuthHeader, ProxyAuthQuery, ProxyAuthBearer, ProxyAuthPath:
		return true
	}
	return false
}

// NeedsName reports whether the placement needs AuthName.
func (a ProxyAuth) NeedsName() bool { return a == ProxyAuthHeader || a == ProxyAuthQuery }

// ProxyConfig is what an owner configures for an endpoint; it never holds
// the secret.
type ProxyConfig struct {
	UpstreamURL  string
	AllowedHosts []string
	Methods      []string
	Paths        []string
	Auth         ProxyAuth
	AuthName     string
	Enabled      bool
	Class        ProxyClass
	// Template is set for the templated classes only.
	Template *ProxyTemplate
	Limits   ProxyLimits
}

// ProxyUsage counts the calls of an endpoint. Nothing about the visitors or
// the requests themselves is kept.
type ProxyUsage struct {
	Requests   int64
	Errors     int64
	Rejected   int64
	LastUsedAt *time.Time
	LastStatus int
	// Today counts the calls of the current UTC day against the budget;
	// Paused tells the budget ran out today.
	Today  int
	Paused bool
}

// ProxyEndpoint is a named secret proxy of a site. SecretEnc is the sealed
// secret ("" when none is set) and must never leave the service.
type ProxyEndpoint struct {
	ID     int64
	SiteID string
	Name   string
	ProxyConfig
	SecretEnc       string
	SecretUpdatedAt *time.Time
	Usage           ProxyUsage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SecretSet reports whether a secret is stored.
func (e ProxyEndpoint) SecretSet() bool { return e.SecretEnc != "" }
