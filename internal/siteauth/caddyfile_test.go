package siteauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

// TestCaddyfileMatchesPaths keeps deploy/Caddyfile in step with this
// package and the service: the forward_auth target, the proxied login path
// and the marker directories. Full validation runs in the e2e suite
// (caddy validate) and in the CI image build.
func TestCaddyfileMatchesPaths(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	cf := string(raw)
	for _, want := range []string{
		"forward_auth @protected control:8081",
		"uri /internal/auth-check",
		"path " + strings.TrimSuffix(Prefix, "/") + " " + Prefix + "*\n",
		"not path " + Prefix + "data " + Prefix + "data/*",
		"@data path " + Prefix + "data " + Prefix + "data/*",
		"not path " + service.ProxyPathPrefix + "*",
		"@proxy path " + service.ProxyPathPrefix + "*",
		"reverse_proxy @proxy control:8080",
		"reverse_proxy @platform control:8080",
		"reverse_proxy @data control:8080",
		"max_size 8KiB",
		"header_down -X-Robots-Tag",
		"root /data/flags/noindex",
		"root /data/flags/protected",
		"@reserved path /_form /_form/*",
		"@unavailable expression {err.status_code} in [502, 503, 504]",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("deploy/Caddyfile lacks %q", want)
		}
	}
	// The secret proxy must be reached only after the access check.
	if i, j := strings.Index(cf, "forward_auth @protected"), strings.Index(cf, "reverse_proxy @proxy"); i < 0 || j < 0 || i > j {
		t.Error("forward_auth must precede the secret proxy")
	}
	// The flags must come before the error/serve rules of the route.
	if i, j := strings.Index(cf, "forward_auth @protected"), strings.Index(cf, "error @hidden 404"); i < 0 || j < 0 || i > j {
		t.Error("forward_auth must precede the error and file_server rules")
	}
	// Site data must never be proxied before the access check.
	if i, j := strings.Index(cf, "forward_auth @protected"), strings.Index(cf, "reverse_proxy @data"); i < 0 || j < 0 || i > j {
		t.Error("site data must be proxied after forward_auth")
	}
	// control believes only the rightmost X-Forwarded-For hop, so every
	// proxy to it must replace the header with the client address.
	if !strings.Contains(cf, "header_up X-Forwarded-For {client_ip}") {
		t.Error("deploy/Caddyfile must send X-Forwarded-For {client_ip} to control")
	}
	for _, line := range strings.Split(cf, "\n") {
		if l := strings.TrimSpace(line); strings.HasPrefix(l, "reverse_proxy") && strings.Contains(l, "control:8080") &&
			!strings.HasSuffix(l, "{") {
			t.Errorf("deploy/Caddyfile: %q must import to_control", l)
		}
	}
	if n, m := strings.Count(cf, "control:8080 {")+strings.Count(cf, "control:8084 {"), strings.Count(cf, "import to_control"); n != m {
		t.Errorf("deploy/Caddyfile: %d proxies to control, %d import to_control", n, m)
	}
	// Protected sites must not be cached by shared caches: the rule comes
	// after the open-site rules, so it replaces them, and 404 pages get it too.
	const protectedCache = `header @protected Cache-Control "private, no-store"`
	asset := strings.Index(cf, `header @asset Cache-Control "public, max-age=3600"`)
	html := strings.Index(cf, "header @html Cache-Control no-cache")
	protected := strings.Index(cf, protectedCache)
	if asset < 0 || html < 0 || protected < asset || protected < html {
		t.Errorf("deploy/Caddyfile must set %s after the @html and @asset rules", protectedCache)
	}
	if strings.Count(cf, protectedCache) != 2 {
		t.Errorf("deploy/Caddyfile must set %s for files and for the custom 404 page", protectedCache)
	}
	if service.VisitorCookieName != "__Host-dropsite_access" {
		t.Errorf("cookie name changed to %q: update docs", service.VisitorCookieName)
	}
}
