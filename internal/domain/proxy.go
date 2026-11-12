package domain

import "time"

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
)

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
}

// ProxyUsage counts the calls of an endpoint. Nothing about the visitors or
// the requests themselves is kept.
type ProxyUsage struct {
	Requests   int64
	Errors     int64
	Rejected   int64
	LastUsedAt *time.Time
	LastStatus int
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
