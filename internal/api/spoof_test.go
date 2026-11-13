package api

import (
	"fmt"
	"net/http"
	"testing"
)

// A visitor in a trusted network (VPN, LAN, another container) reaches the
// site through Caddy, which appends the visitor's own address. Whatever the
// visitor prepends must not become a fresh rate-limit key.
func TestFormLimitIgnoresPrependedForwardedHops(t *testing.T) {
	f := newFormHarness(t)
	viaCaddy := func(xff string) reqOpt {
		return func(r *http.Request) {
			r.RemoteAddr = "172.18.0.2:40000" // the Caddy container, trusted by default
			r.Header.Set("X-Forwarded-For", xff)
		}
	}
	limited := false
	for i := range 50 {
		spoof := fmt.Sprintf("6.6.%d.%d, 10.1.1.1", i/250, i%250+1)
		rec := f.visitor(http.MethodPost, "/_form/lead", formCT, "a=1", viaCaddy(spoof))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("prepended X-Forwarded-For hops bypassed the per-IP form limit")
	}
	// Another real client behind the same proxy keeps its own budget.
	rec := f.visitor(http.MethodPost, "/_form/lead", formCT, "a=1", viaCaddy("10.1.1.1, 10.1.1.2"))
	if rec.Code == http.StatusTooManyRequests {
		t.Fatalf("a different client was limited: %d %s", rec.Code, rec.Body.String())
	}
}
