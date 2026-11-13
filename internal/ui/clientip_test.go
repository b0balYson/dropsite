package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The admin listener has no proxy in front: X-Forwarded-For is never read,
// even from a peer that the public listener would trust.
func TestAdminClientIPIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 10.1.1.1")
	if got := clientIP(r); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want the peer address", got)
	}
}

// Rotating X-Forwarded-For values does not reset the login limit.
func TestLoginLimitIgnoresForwardedFor(t *testing.T) {
	h := newHarness(t)
	var p page
	for i := range 11 {
		p = h.submit("/login", "/login", url.Values{"email": {"x@pp.test"}, "password": {"wrong-password-1"}},
			"X-Forwarded-For", fmt.Sprintf("6.6.6.%d", i+1))
	}
	wantStatus(t, p, http.StatusTooManyRequests)
}
