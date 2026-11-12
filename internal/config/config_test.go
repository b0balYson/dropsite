package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(kv map[string]string) Getenv {
	return func(k string) (string, bool) {
		v, ok := kv[k]
		return v, ok
	}
}

func base() map[string]string {
	return map[string]string{"BASE_DOMAIN": "Example.COM"}
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseDomain != "example.com" || c.APIHost != "api.example.com" {
		t.Fatalf("domains: %q %q", c.BaseDomain, c.APIHost)
	}
	if c.TLSMode != TLSModeOnDemand || c.LogLevel != slog.LevelInfo {
		t.Fatal("bad defaults")
	}
	l := c.Limits
	if l.MaxSiteBytes != 50<<20 || l.MaxFileBytes != 20<<20 || l.MaxFiles != 2000 || l.MaxSitesPerUser != 10 || l.KeepReleases != 5 {
		t.Fatalf("bad limits %+v", l)
	}
	if c.SitesDir != "/data" || c.StateDir != "/state" || c.PublicAddr != ":8080" || c.InternalAddr != ":8081" || c.AdminAddr != ":8082" {
		t.Fatal("bad paths or addresses")
	}
	if c.UIURL != "http://127.0.0.1:8082" || len(c.TrustedProxies) == 0 {
		t.Fatal("bad ui url or proxies")
	}
}

func TestOverrides(t *testing.T) {
	e := base()
	e["API_HOST"] = "deploy.example.com"
	e["MAX_SITE_BYTES"] = "100MiB"
	e["MAX_FILE_BYTES"] = "1000000"
	e["MAX_FILES"] = "50"
	e["KEEP_RELEASES"] = "2"
	e["RESERVED_SLUGS"] = " Shop, blog ,"
	e["LOG_LEVEL"] = "debug"
	e["UI_URL"] = "http://10.0.0.5:8082/"
	e["BASE_DOMAIN"] = "пример.рф"
	c, err := Load(env(e))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseDomain != "xn--e1afmkfd.xn--p1ai" {
		t.Fatalf("idn: %q", c.BaseDomain)
	}
	if c.Limits.MaxSiteBytes != 100<<20 || c.Limits.MaxFileBytes != 1000000 || c.Limits.MaxFiles != 50 || c.Limits.KeepReleases != 2 {
		t.Fatalf("limits %+v", c.Limits)
	}
	if strings.Join(c.ReservedSlugs, ",") != "shop,blog" || c.LogLevel != slog.LevelDebug || c.UIURL != "http://10.0.0.5:8082" {
		t.Fatal("bad overrides")
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{"missing base domain", "BASE_DOMAIN", "", "BASE_DOMAIN: is required"},
		{"bad base domain", "BASE_DOMAIN", "../etc", "BASE_DOMAIN"},
		{"single label", "BASE_DOMAIN", "localhost", "BASE_DOMAIN"},
		{"ip address", "BASE_DOMAIN", "10.0.0.5", "BASE_DOMAIN"},
		{"max files text", "MAX_FILES", "abc", "MAX_FILES"},
		{"max files zero", "MAX_FILES", "0", "MAX_FILES"},
		{"bad size", "MAX_SITE_BYTES", "lots", "MAX_SITE_BYTES"},
		{"keep one", "KEEP_RELEASES", "1", "KEEP_RELEASES"},
		{"wildcard", "TLS_MODE", "wildcard", "docs/tls.md"},
		{"unknown tls", "TLS_MODE", "manual", "TLS_MODE"},
		{"log level", "LOG_LEVEL", "loud", "LOG_LEVEL"},
		{"admin email", "ADMIN_EMAIL", "nope", "ADMIN_EMAIL"},
		{"admin password", "ADMIN_PASSWORD", "short", "ADMIN_PASSWORD"},
		{"reserved", "RESERVED_SLUGS", "a.b", "RESERVED_SLUGS"},
		{"ui url", "UI_URL", "ftp://x", "UI_URL"},
		{"proxies", "TRUSTED_PROXIES", "nope", "TRUSTED_PROXIES"},
		{"file over site", "MAX_FILE_BYTES", "60MiB", "MAX_FILE_BYTES"},
		{"https port", "SITES_HTTPS_PORT", "70000", "SITES_HTTPS_PORT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base()
			e[tc.key] = tc.val
			_, err := Load(env(e))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{"1": 1, "50MB": 50_000_000, "50MiB": 50 << 20, "20m": 20 << 20, "1G": 1 << 30, "10 KB": 10_000, "7B": 7}
	for in, want := range cases {
		got, err := ParseBytes(in)
		if err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "-1", "1.5MB", "99999999999GiB"} {
		if _, err := ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) should fail", bad)
		}
	}
}

// A UI on the sites domain would let any published site set a cookie with
// Domain=<base> and fix the admin session.
func TestUIURLMustNotBeOnBaseDomain(t *testing.T) {
	bad := []string{
		"https://example.com", "https://EXAMPLE.com.", "https://admin.example.com",
		"https://a.b.example.com:8443", "http://xn--e1afmkfd.example.com", "https://пример.example.com",
	}
	for _, u := range bad {
		e := base()
		e["UI_URL"] = u
		if _, err := Load(env(e)); err == nil || !strings.Contains(err.Error(), "UI_URL") {
			t.Errorf("%s: want UI_URL error, got %v", u, err)
		}
	}
	good := []string{"https://admin.example.org", "https://notexample.com", "https://example.com.evil.org", "http://10.0.0.5:8082"}
	for _, u := range good {
		e := base()
		e["UI_URL"] = u
		if _, err := Load(env(e)); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}
