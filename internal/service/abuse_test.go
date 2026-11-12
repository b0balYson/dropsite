package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestIPKeyAggregatesIPv6By64(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"2001:db8:1:2::1", "2001:db8:1:2:ffff:ffff:ffff:ffff"},
		{"2001:db8:1:2::1%eth0", "2001:db8:1:2::2"},
		{"::ffff:198.51.100.7", "198.51.100.7"},
	} {
		if ipKey(c.a) != ipKey(c.b) {
			t.Errorf("%s and %s: %q != %q", c.a, c.b, ipKey(c.a), ipKey(c.b))
		}
	}
	for _, c := range []struct{ a, b string }{
		{"2001:db8:1:2::1", "2001:db8:1:3::1"},
		{"198.51.100.7", "198.51.100.8"},
	} {
		if ipKey(c.a) == ipKey(c.b) {
			t.Errorf("%s and %s share %q", c.a, c.b, ipKey(c.a))
		}
	}
	if ipKey("not-an-ip") != "ip:not-an-ip" {
		t.Errorf("fallback: %q", ipKey("not-an-ip"))
	}
}

func TestRegistrationLimitPerIPv6Network(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range RegistrationsPerIP {
		if _, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a.example/cb"}, fmt.Sprintf("2001:db8:5:6::%x", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a.example/cb"}, "2001:db8:5:6:abcd::1")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("same /64: %v", err)
	}
	if _, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a.example/cb"}, "2001:db8:5:7::1"); err != nil {
		t.Fatalf("other /64: %v", err)
	}
}

func TestRegistrationCapEvictsUnusedClients(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range MaxOAuthClients {
		if _, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a.example/cb"}, fmt.Sprintf("10.%d.%d.1", i/200, i%200)); err != nil {
			t.Fatal(err)
		}
	}
	// Within the grace period nothing can be evicted.
	_, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a.example/cb"}, "10.250.0.1")
	wantOAuth(t, err, "temporarily_unavailable")
	e.clock.Advance(ClientEvictionGrace + time.Second)
	if _, err := e.svc.RegisterOAuthClient(ctx, "x", []string{"https://a.example/cb"}, "10.250.0.2"); err != nil {
		t.Fatalf("at cap: %v", err)
	}
	if n, err := e.repo.CountOAuthClients(ctx); err != nil || n != MaxOAuthClients {
		t.Fatalf("count at cap: %d %v", n, err)
	}
	// Never-used registrations are purged after NeverUsedClientTTL.
	e.clock.Advance(NeverUsedClientTTL + time.Second)
	if err := e.svc.housekeepConnect(ctx, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if n, err := e.repo.CountOAuthClients(ctx); err != nil || n != 0 {
		t.Fatalf("after purge: %d %v", n, err)
	}
}

func TestConnectCodeFailureAuditIsAggregated(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range FailedCodesPerIP {
		_, err := e.svc.ExchangeConnectCode(ctx, "AAAA-AAAA-AAAAA", "", fmt.Sprintf("2001:db8:9:9::%x", i+1))
		wantCode(t, err, domain.CodeConnectInvalid)
	}
	_, err := e.svc.ExchangeConnectCode(ctx, "AAAA-AAAA-AAAAA", "", "2001:db8:9:9::99")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("limit per /64: %v", err)
	}
	if n := len(e.auditMeta("connect.code_failed")); n != 1 {
		t.Fatalf("audit rows: %d", n)
	}
	e.clock.Advance(FailedCodesWindow + time.Second)
	_, _ = e.svc.ExchangeConnectCode(ctx, "AAAA-AAAA-AAAAA", "", "2001:db8:9:9::1")
	if n := len(e.auditMeta("connect.code_failed")); n != 2 {
		t.Fatalf("audit rows after window: %d", n)
	}
}

func TestCleanClientName(t *testing.T) {
	long := strings.Repeat("я", maxClientNameRunes+10)
	for in, want := range map[string]string{
		"  Chat \t App ":         "Chat App",
		"Evil\u202egnp.exe":      "Evilgnp.exe",
		"a\u200bb\u2066c\u0000d": "abcd",
		"line\nbreak":            "line break",
		long:                     strings.Repeat("я", maxClientNameRunes),
		"\u202e\u202e":           "",
		"ok\xffname":             "okname",
	} {
		if got := CleanClientName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestClientMetadataFetchLimit(t *testing.T) {
	e := newEnv(t)
	for range ClientMetadataFetchesPerIP {
		if err := e.svc.AllowClientMetadataFetch("2001:db8::1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.AllowClientMetadataFetch("2001:db8::2"); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("want limit, got %v", err)
	}
	if err := e.svc.AllowClientMetadataFetch("198.51.100.1"); err != nil {
		t.Fatal(err)
	}
}

// Concurrent exchanges of one code: exactly one succeeds and the replay
// always revokes what it issued.
func TestCodeReplayRaceAlwaysRevokes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	for range 20 {
		code := e.authorize(t, u, "client-1")
		var wg sync.WaitGroup
		results := make([]OAuthTokens, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() {
				results[i], errs[i] = e.svc.ExchangeOAuthCode(ctx, code, verifier, "client-1", "https://chat.example/cb", "")
			})
		}
		wg.Wait()
		ok := 0
		for i := range 2 {
			if errs[i] != nil {
				wantOAuth(t, errs[i], "invalid_grant")
				continue
			}
			ok++
			if _, _, err := e.svc.AuthenticateMCP(ctx, results[i].AccessToken, "1.1.1.1"); err == nil {
				t.Fatal("token issued for a replayed code still works")
			}
		}
		if ok != 1 {
			t.Fatalf("successes: %d (%v)", ok, errs)
		}
	}
}

func TestValidRedirectURIHost(t *testing.T) {
	for u, ok := range map[string]bool{
		"https://chat.example/cb":          true,
		"https://Chat.Example:8443/cb":     true,
		"https://203.0.113.5/cb":           true,
		"https://[2001:db8::1]/cb":         true,
		"http://127.0.0.1:3000/cb":         true,
		"http://[::1]:3000/cb":             true,
		"http://localhost:3000/cb":         true,
		"https://xn--80ak6aa92e.example/c": true,
		"https://*/cb":                     false,
		"https://a.com;script-src/cb":      false,
		"https://'self'/cb":                false,
		"https://a.com%20b/cb":             false,
		"https://a_b.example/cb":           false,
		"https://-a.example/cb":            false,
		"https://[fe80::1%25eth0]/cb":      false,
	} {
		if got := ValidRedirectURI(u); got != ok {
			t.Errorf("%s: got %v", u, got)
		}
	}
	if o := RedirectOrigin("https://Chat.Example:8443/cb?x=1"); o != "https://Chat.Example:8443" {
		t.Errorf("origin: %q", o)
	}
	if o := RedirectOrigin("https://a.com;script-src/cb"); o != "" {
		t.Errorf("invalid origin: %q", o)
	}
}
