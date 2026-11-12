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
		"path " + strings.TrimSuffix(Prefix, "/") + " " + Prefix + "*",
		"not path " + service.ProxyPathPrefix + "*",
		"@proxy path " + service.ProxyPathPrefix + "*",
		"reverse_proxy @proxy control:8080",
		"reverse_proxy @platform control:8080",
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
	if service.VisitorCookieName != "__Host-dropsite_access" {
		t.Errorf("cookie name changed to %q: update docs", service.VisitorCookieName)
	}
}
