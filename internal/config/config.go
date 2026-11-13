// Package config reads and validates the control server configuration from
// environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Defaults from the PRD (section 6).
const (
	DefaultMaxSiteBytes    int64 = 50 << 20
	DefaultMaxFileBytes    int64 = 20 << 20
	DefaultMaxFiles              = 2000
	DefaultMaxSitesPerUser       = 10
	DefaultKeepReleases          = 5
	// MinKeepReleases guarantees the previous release survives a switch, so a
	// visitor never resolves a symlink into a deleted directory.
	MinKeepReleases = 2

	// DefaultStatsLogFile is where Caddy writes the sites access log.
	DefaultStatsLogFile  = "/logs/sites-access.log"
	DefaultStatsInterval = time.Minute
	minStatsInterval     = time.Second
	maxStatsInterval     = time.Hour

	// DefaultAdminAddr keeps a binary started outside Docker reachable from
	// this machine only; compose sets ADMIN_ADDR=:8082 inside the container.
	DefaultAdminAddr = "127.0.0.1:8082"
	// DefaultPublicAdminAddr is the listener Caddy proxies
	// https://admin.<base domain> to. It is never published to the host:
	// compose sets PUBLIC_ADMIN_ADDR=:8084 inside the container network.
	DefaultPublicAdminAddr = "127.0.0.1:8084"

	TLSModeOnDemand = "ondemand"
	TLSModeWildcard = "wildcard"
)

// Config is the validated configuration.
type Config struct {
	BaseDomain    string
	APIHost       string
	TLSMode       string
	AdminEmail    string
	AdminPassword string
	UIURL         string
	// SitesHTTPSPort is the port visitors use; non-443 only in local development.
	SitesHTTPSPort int

	Limits        domain.Limits
	ReservedSlugs []string
	LogLevel      slog.Level

	SitesDir string // root of the sites volume (contains sites/, by-host/, empty/)
	StateDir string // database and temporary uploads

	PublicAddr   string
	InternalAddr string
	AdminAddr    string
	// AdminBindIP is the host address the admin UI is published on when
	// control runs in a container (compose passes ADMIN_BIND_IP); empty
	// outside Docker, where AdminAddr itself tells where the UI listens.
	AdminBindIP string
	// AllowPublicAdmin lets the admin UI start on a public address
	// (ALLOW_PUBLIC_ADMIN=1); without it control refuses to start.
	AllowPublicAdmin bool
	// PublicAdminAddr is the listener of the public admin interface behind
	// Caddy (https://admin.<base domain>). It answers 404 to everything
	// until an administrator turns public access on in the settings.
	PublicAdminAddr string

	TrustedProxies []netip.Prefix

	// MaxDomainsPerSite caps external domains per site (any status).
	MaxDomainsPerSite int
	// ServerIPs are the public addresses external domains must point at; when
	// empty, the DNS check uses the addresses of BASE_DOMAIN and API_HOST.
	ServerIPs []netip.Addr
	// StatsLogFile is the sites access log read for visit statistics; empty
	// disables collection. StatsInterval is how often it is read.
	StatsLogFile  string
	StatsInterval time.Duration
	// TelegramAPIURL is the Bot API base URL (overridable for tests).
	TelegramAPIURL string
	// FormWebhookAllow lists networks that form webhooks may reach although
	// they are private (test stacks only); plain http is allowed for them.
	FormWebhookAllow []netip.Prefix
	// PublishOptimize allows publish-time optimization (PUBLISH_OPTIMIZE,
	// on by default); sites may still turn it off for themselves.
	PublishOptimize bool
	// Renderer is the optional screenshot renderer.
	Renderer Renderer
	// UpdateCheck turns on the periodic check for a new release on GitHub
	// (UPDATE_CHECK, on by default; docs/install.md, "Updating").
	UpdateCheck bool
	// UpdateCheckURL is the release endpoint the check asks; it is changed
	// only by tests (UPDATE_CHECK_URL).
	UpdateCheckURL string
}

// DefaultUpdateCheckURL is the newest published release of dropsite; GitHub
// never answers a draft or a pre-release here.
const DefaultUpdateCheckURL = "https://api.github.com/repos/b0balYson/dropsite/releases/latest"

// maxDomainsPerSiteLimit bounds MAX_DOMAINS_PER_SITE.
const maxDomainsPerSiteLimit = 100

// Wildcard reports whether sites under the base domain use one wildcard
// certificate (TLS_MODE=wildcard); release previews exist only in this mode.
func (c *Config) Wildcard() bool { return c.TLSMode == TLSModeWildcard }

// Getenv is the environment lookup function, injectable for tests.
type Getenv func(string) (string, bool)

type reader struct {
	get  Getenv
	errs []error
}

func (r *reader) str(key, def string) string {
	if v, ok := r.get(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *reader) fail(key, format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf("%s: "+format, append([]any{key}, args...)...))
}

func (r *reader) positiveInt(key string, def int) int {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		r.fail(key, "must be a positive integer, got %q", raw)
		return def
	}
	return n
}

func (r *reader) bytes(key string, def int64) int64 {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	n, err := ParseBytes(raw)
	if err != nil || n <= 0 {
		r.fail(key, "must be a positive size such as 52428800, 50MB or 50MiB, got %q", raw)
		return def
	}
	return n
}

// Load reads configuration using get (typically os.LookupEnv).
func Load(get Getenv) (*Config, error) {
	r := &reader{get: get}
	c := &Config{
		TLSMode:         strings.ToLower(r.str("TLS_MODE", TLSModeOnDemand)),
		AdminEmail:      r.str("ADMIN_EMAIL", ""),
		AdminPassword:   r.str("ADMIN_PASSWORD", ""),
		SitesDir:        r.str("SITES_DIR", "/data"),
		StateDir:        r.str("STATE_DIR", "/state"),
		PublicAddr:      r.str("PUBLIC_ADDR", ":8080"),
		InternalAddr:    r.str("INTERNAL_ADDR", ":8081"),
		AdminAddr:       r.str("ADMIN_ADDR", DefaultAdminAddr),
		PublicAdminAddr: r.str("PUBLIC_ADMIN_ADDR", DefaultPublicAdminAddr),
		Limits: domain.Limits{
			MaxSiteBytes:    r.bytes("MAX_SITE_BYTES", DefaultMaxSiteBytes),
			MaxFileBytes:    r.bytes("MAX_FILE_BYTES", DefaultMaxFileBytes),
			MaxFiles:        r.positiveInt("MAX_FILES", DefaultMaxFiles),
			MaxSitesPerUser: r.positiveInt("MAX_SITES_PER_USER", DefaultMaxSitesPerUser),
			KeepReleases:    r.positiveInt("KEEP_RELEASES", DefaultKeepReleases),
		},
	}

	c.BaseDomain = r.hostname("BASE_DOMAIN", "", true)
	c.APIHost = r.hostname("API_HOST", "api."+c.BaseDomain, false)
	c.UIURL = r.uiURL(c.BaseDomain)
	c.SitesHTTPSPort = r.positiveInt("SITES_HTTPS_PORT", 443)
	if c.SitesHTTPSPort > 65535 {
		r.fail("SITES_HTTPS_PORT", "must be a TCP port")
	}
	c.LogLevel = r.logLevel()
	c.ReservedSlugs = splitList(r.str("RESERVED_SLUGS", ""))
	c.StatsLogFile = r.str("STATS_LOG_FILE", DefaultStatsLogFile)
	if strings.EqualFold(c.StatsLogFile, "off") {
		c.StatsLogFile = ""
	}
	c.StatsInterval = r.duration("STATS_INTERVAL", DefaultStatsInterval, minStatsInterval, maxStatsInterval)
	c.TrustedProxies = r.prefixes("TRUSTED_PROXIES", "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128")
	c.MaxDomainsPerSite = r.positiveInt("MAX_DOMAINS_PER_SITE", domain.DefaultMaxDomainsPerSite)
	if c.MaxDomainsPerSite > maxDomainsPerSiteLimit {
		r.fail("MAX_DOMAINS_PER_SITE", "must be at most %d", maxDomainsPerSiteLimit)
	}
	c.ServerIPs = r.addrs("SERVER_IPS")
	c.TelegramAPIURL = r.httpURL("TELEGRAM_API_URL", "https://api.telegram.org")
	c.FormWebhookAllow = r.prefixes("FORM_WEBHOOK_ALLOW_CIDRS", "")
	c.PublishOptimize = r.onOff("PUBLISH_OPTIMIZE", true)
	c.AdminBindIP = r.ipAddr("ADMIN_BIND_IP")
	c.AllowPublicAdmin = r.onOff("ALLOW_PUBLIC_ADMIN", false)
	c.Renderer = r.renderer()
	c.UpdateCheck = r.onOff("UPDATE_CHECK", true)
	c.UpdateCheckURL = r.httpURL("UPDATE_CHECK_URL", DefaultUpdateCheckURL)

	// The DNS provider token is deliberately not read here: only Caddy needs
	// it, and compose refuses to start the wildcard override without it.
	switch c.TLSMode {
	case TLSModeOnDemand, TLSModeWildcard:
	default:
		r.fail("TLS_MODE", "must be %q or %q, got %q (see docs/tls.md)", TLSModeOnDemand, TLSModeWildcard, c.TLSMode)
	}
	if c.Limits.KeepReleases < MinKeepReleases {
		r.fail("KEEP_RELEASES", "must be at least %d", MinKeepReleases)
	}
	if c.Limits.MaxFileBytes > c.Limits.MaxSiteBytes {
		r.fail("MAX_FILE_BYTES", "must not exceed MAX_SITE_BYTES")
	}
	if c.AdminEmail != "" {
		if _, err := domain.NormalizeEmail(c.AdminEmail); err != nil {
			r.fail("ADMIN_EMAIL", "invalid email address")
		}
	}
	if c.AdminPassword != "" {
		if err := domain.ValidatePassword(c.AdminPassword); err != nil {
			r.fail("ADMIN_PASSWORD", "must be %d-%d characters", domain.MinPasswordLength, domain.MaxPasswordLength)
		}
	}
	for _, s := range c.ReservedSlugs {
		if !domain.IsValidHostname(s) || strings.Contains(s, ".") {
			r.fail("RESERVED_SLUGS", "%q is not a valid slug", s)
		}
	}
	if err := errors.Join(r.errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return c, nil
}

func (r *reader) hostname(key, def string, required bool) string {
	raw := r.str(key, def)
	if raw == "" || raw == "api." {
		if required {
			r.fail(key, "is required")
		}
		return ""
	}
	ascii, err := idna.Lookup.ToASCII(domain.NormalizeHostname(raw))
	if err != nil || !domain.IsValidHostname(ascii) || !strings.Contains(ascii, ".") || numericTLD(ascii) {
		r.fail(key, "must be a domain name such as example.com, got %q", raw)
		return ""
	}
	return ascii
}

// numericTLD rejects IP-like names: Caddy's certificate permission check must
// never find a by-host entry named like an address.
func numericTLD(h string) bool {
	tld := h[strings.LastIndexByte(h, '.')+1:]
	return strings.Trim(tld, "0123456789") == ""
}

// uiURL reads UI_URL. The UI must not live on the base domain or below it:
// published sites run arbitrary JavaScript there and could set a cookie with
// Domain=<base domain> to fix an admin session.
func (r *reader) uiURL(baseDomain string) string {
	raw := strings.TrimSuffix(r.str("UI_URL", "http://127.0.0.1:8082"), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		r.fail("UI_URL", "must be an absolute URL such as http://10.0.0.5:8082, got %q", raw)
		return raw
	}
	if baseDomain != "" && underDomain(u.Hostname(), baseDomain) {
		r.fail("UI_URL", "must not be on BASE_DOMAIN %s or its subdomains (published sites could set cookies for it), got %q",
			baseDomain, raw)
	}
	return raw
}

// underDomain reports whether host equals domain or is a subdomain of it,
// comparing the ASCII (punycode) forms case-insensitively.
func underDomain(host, domainName string) bool {
	h := domain.NormalizeHostname(host)
	if ascii, err := idna.Lookup.ToASCII(h); err == nil {
		h = ascii
	}
	return h == domainName || strings.HasSuffix(h, "."+domainName)
}

// httpURL reads an absolute http(s) URL without a trailing slash.
func (r *reader) httpURL(key, def string) string {
	raw := strings.TrimSuffix(r.str(key, def), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		r.fail(key, "must be an absolute http(s) URL, got %q", raw)
		return def
	}
	return raw
}

func (r *reader) logLevel() slog.Level {
	raw := r.str("LOG_LEVEL", "info")
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		r.fail("LOG_LEVEL", "must be debug, info, warn or error, got %q", raw)
		return slog.LevelInfo
	}
	return lvl
}

func (r *reader) duration(key string, def, lo, hi time.Duration) time.Duration {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < lo || d > hi {
		r.fail(key, "must be a duration between %s and %s such as 1m, got %q", lo, hi, raw)
		return def
	}
	return d
}

func (r *reader) prefixes(key, def string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range splitList(r.str(key, def)) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			r.fail(key, "invalid CIDR %q", s)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// ipAddr reads an optional single IP address; empty when unset.
func (r *reader) ipAddr(key string) string {
	raw := r.str(key, "")
	if raw == "" {
		return ""
	}
	a, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	if err != nil {
		r.fail(key, "must be an IP address such as 127.0.0.1, got %q", raw)
		return ""
	}
	return a.Unmap().String()
}

func (r *reader) addrs(key string) []netip.Addr {
	var out []netip.Addr
	for _, s := range splitList(r.str(key, "")) {
		a, err := netip.ParseAddr(s)
		if err != nil {
			r.fail(key, "invalid IP address %q", s)
			continue
		}
		out = append(out, a.Unmap())
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseBytes parses a byte size: plain integer, or a number with KB/MB/GB
// (decimal) or KiB/MiB/GiB, K/M/G (binary) suffix.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   int64
	}{
		{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30},
		{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
		{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"B", 1},
	}
	upper := strings.ToUpper(s)
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(upper, u.suffix) {
			mult = u.mult
			upper = strings.TrimSpace(strings.TrimSuffix(upper, u.suffix))
			break
		}
	}
	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", s, err)
	}
	if n < 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("size %q out of range", s)
	}
	return n * mult, nil
}
