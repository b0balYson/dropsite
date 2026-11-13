package service

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Secret proxy configuration bounds.
const (
	ProxyEndpointsPerSite = 10
	proxyMaxURLLen        = 2048
	proxyMaxHosts         = 10
	proxyMaxPaths         = 10
	proxyMaxPathLen       = 200
	proxyMaxSecretLen     = 4096
)

var (
	proxyNameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	proxyHeaderRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
	proxyParamRe     = regexp.MustCompile(`^[A-Za-z0-9_.\-\[\]]{1,64}$`)
	proxyDefaultVerb = []string{"GET", "POST"}
	proxyMethods     = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
)

// proxyHeadersReserved are header names a secret must not be placed in:
// they are set by the proxy or the HTTP layer, or are stripped on purpose.
var proxyHeadersReserved = map[string]bool{
	"host": true, "content-length": true, "content-type": true, "transfer-encoding": true, "connection": true,
	"keep-alive": true, "te": true, "trailer": true, "upgrade": true, "proxy-connection": true, "cookie": true,
	"accept-encoding": true, "origin": true, "referer": true, "forwarded": true, "proxy-authorization": true,
	"authorization": true, "user-agent": true, "expect": true, "via": true, "x-real-ip": true,
	"x-http-method-override": true, "x-http-method": true, "x-method-override": true, "x-original-method": true,
	"x-original-url": true, "x-rewrite-url": true,
}

// proxySecretParams are query parameter names that look like a credential;
// a base URL carrying one is refused, the key belongs in the web interface.
var proxySecretParams = []string{"key", "token", "secret", "password", "passwd", "signature", "credential"}

func proxyInvalid(msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeProxyInvalid, msg)
}

// errProxyName is returned for a malformed endpoint name.
var errProxyName = proxyInvalid("the name must be 1-40 characters: a-z, 0-9 and hyphens, starting with a letter or digit")

// normalizeProxyName validates an endpoint name.
func normalizeProxyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !proxyNameRe.MatchString(name) {
		return "", errProxyName
	}
	return name, nil
}

// looksLikeSecretParam reports whether a query parameter name suggests a
// credential (api_key, access_token, X-Api-Key...).
func looksLikeSecretParam(name string) bool {
	n := strings.ToLower(name)
	for _, w := range proxySecretParams {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// forbiddenProxyHost reports hosts the proxy must never reach: the
// platform itself and anything local.
func (s *Service) forbiddenProxyHost(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") ||
		strings.HasSuffix(h, ".internal") || !strings.Contains(h, ".") {
		return true
	}
	for _, own := range []string{s.cfg.BaseDomain, s.cfg.APIHost, hostOfURL(s.cfg.UIURL)} {
		own = strings.ToLower(strings.TrimSuffix(own, "."))
		if own != "" && (h == own || strings.HasSuffix(h, "."+own)) {
			return true
		}
	}
	return false
}

func hostOfURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// normalizeProxyHost validates one exact upstream host.
func (s *Service) normalizeProxyHost(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if _, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil {
		return "", proxyInvalid("use a host name, not an IP address: " + raw)
	}
	if !domain.IsValidHostname(h) || len(h) > 253 {
		return "", proxyInvalid("not a valid host name (no scheme, port, path or wildcard): " + raw)
	}
	if s.forbiddenProxyHost(h) {
		return "", proxyInvalid("this host cannot be used by the proxy (local, internal or this server): " + raw)
	}
	return h, nil
}

// parseProxyUpstream checks the upstream base URL and returns it in
// canonical form with its host.
func (s *Service) parseProxyUpstream(raw string, auth domain.ProxyAuth) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > proxyMaxURLLen || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return "", "", proxyInvalid("upstream_url must be an https:// address without spaces, at most 2048 characters")
	}
	// The secret mark is not valid in a URL path as is; swap it for a token
	// that survives parsing and restore it afterwards.
	const token = "dropsite-secret-mark"
	marks := strings.Count(raw, domain.ProxySecretMark)
	u, err := url.Parse(strings.Replace(raw, domain.ProxySecretMark, token, 1))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return "", "", proxyInvalid("upstream_url must be a full https:// address")
	}
	if u.User != nil {
		return "", "", proxyInvalid("upstream_url must not contain a user name or password")
	}
	if u.Port() != "" && u.Port() != "443" {
		return "", "", proxyInvalid("upstream_url must use the standard https port")
	}
	for name := range u.Query() {
		if looksLikeSecretParam(name) {
			return "", "", domain.E(domain.KindInvalid, domain.CodeProxyInvalid,
				"upstream_url must not contain a key ("+name+"); the user pastes the key in the web interface").
				WithParams(map[string]any{"param": name})
		}
	}
	switch {
	case auth == domain.ProxyAuthPath && (marks != 1 || !strings.Contains(u.EscapedPath(), token)):
		return "", "", proxyInvalid("with auth \"path\" the upstream_url path must contain {secret} exactly once")
	case auth != domain.ProxyAuthPath && marks > 0:
		return "", "", proxyInvalid("{secret} in upstream_url needs auth \"path\"")
	}
	host, err := s.normalizeProxyHost(u.Hostname())
	if err != nil {
		return "", "", err
	}
	u.Host, u.Fragment, u.RawFragment = host, "", ""
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	out := strings.Replace(u.String(), token, domain.ProxySecretMark, 1)
	return out, host, nil
}

// normalizeProxyConfig validates c and fills defaults.
func (s *Service) normalizeProxyConfig(c domain.ProxyConfig) (domain.ProxyConfig, error) {
	out := domain.ProxyConfig{Enabled: c.Enabled, Auth: domain.ProxyAuth(strings.ToLower(strings.TrimSpace(string(c.Auth))))}
	if !out.Auth.Valid() {
		return domain.ProxyConfig{}, proxyInvalid("auth must be header, query, bearer or path")
	}
	upstream, host, err := s.parseProxyUpstream(c.UpstreamURL, out.Auth)
	if err != nil {
		return domain.ProxyConfig{}, err
	}
	out.UpstreamURL = upstream
	if out.AllowedHosts, err = s.normalizeProxyHosts(host, c.AllowedHosts); err != nil {
		return domain.ProxyConfig{}, err
	}
	if out.Methods, err = normalizeProxyMethods(c.Methods); err != nil {
		return domain.ProxyConfig{}, err
	}
	if out.Paths, err = normalizeProxyPaths(c.Paths); err != nil {
		return domain.ProxyConfig{}, err
	}
	if out.AuthName, err = normalizeProxyAuthName(out.Auth, c.AuthName); err != nil {
		return domain.ProxyConfig{}, err
	}
	if err := applyProxyClass(&out, c); err != nil {
		return domain.ProxyConfig{}, err
	}
	return out, nil
}

func (s *Service) normalizeProxyHosts(upstream string, hosts []string) ([]string, error) {
	out := []string{upstream}
	for _, h := range hosts {
		if strings.TrimSpace(h) == "" {
			continue
		}
		n, err := s.normalizeProxyHost(h)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(out) > proxyMaxHosts {
		return nil, proxyInvalid("at most 10 allowed hosts")
	}
	return out, nil
}

func normalizeProxyMethods(in []string) ([]string, error) {
	if len(in) == 0 {
		return slices.Clone(proxyDefaultVerb), nil
	}
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.ToUpper(strings.TrimSpace(m))
		if !slices.Contains(proxyMethods, m) {
			return nil, proxyInvalid("methods may be GET, HEAD, POST, PUT, PATCH and DELETE")
		}
		seen[m] = true
	}
	out := []string{}
	for _, m := range proxyMethods {
		if seen[m] {
			out = append(out, m)
		}
	}
	return out, nil
}

func normalizeProxyPaths(in []string) ([]string, error) {
	out := []string{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") || len(p) > proxyMaxPathLen || strings.ContainsAny(p, "?#\\%") ||
			strings.IndexFunc(p, unicode.IsSpace) >= 0 || strings.IndexFunc(p, unicode.IsControl) >= 0 {
			return nil, proxyInvalid("each path must start with / and contain no spaces, ?, #, % or \\: " + p)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "." || seg == ".." {
				return nil, proxyInvalid("paths must not contain . or .. segments: " + p)
			}
		}
		if p != "/" {
			p = strings.TrimSuffix(p, "/")
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) > proxyMaxPaths {
		return nil, proxyInvalid("at most 10 paths")
	}
	return out, nil
}

func normalizeProxyAuthName(auth domain.ProxyAuth, name string) (string, error) {
	name = strings.TrimSpace(name)
	switch auth {
	case domain.ProxyAuthHeader:
		if !proxyHeaderRe.MatchString(name) || proxyHeadersReserved[strings.ToLower(name)] ||
			strings.HasPrefix(strings.ToLower(name), "sec-") || strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
			return "", proxyInvalid("auth_name must be a header name such as X-Api-Key (not Authorization, Cookie or Host)")
		}
	case domain.ProxyAuthQuery:
		if !proxyParamRe.MatchString(name) {
			return "", proxyInvalid("auth_name must be a query parameter name such as api_key")
		}
	default:
		return "", nil
	}
	return name, nil
}

// normalizeProxySecret validates a pasted secret.
func normalizeProxySecret(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" || len(secret) > proxyMaxSecretLen || strings.IndexFunc(secret, unicode.IsControl) >= 0 {
		return "", domain.E(domain.KindInvalid, domain.CodeProxySecret,
			"the key must be 1-4096 characters on one line")
	}
	return secret, nil
}

// proxyDestinationChanged reports whether b sends the secret somewhere else
// or differently than a, which invalidates a stored secret.
func proxyDestinationChanged(a, b domain.ProxyConfig) bool {
	return a.UpstreamURL != b.UpstreamURL || a.Auth != b.Auth || a.AuthName != b.AuthName ||
		!slices.Equal(sortedCopy(a.AllowedHosts), sortedCopy(b.AllowedHosts))
}

func sortedCopy(v []string) []string {
	c := slices.Clone(v)
	slices.Sort(c)
	return c
}
