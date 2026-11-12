package service

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/b0balYson/dropsite/internal/netguard"
)

// Upstream timeouts of the secret proxy.
const (
	ProxyTimeout             = 60 * time.Second
	proxyDialTimeout         = 5 * time.Second
	proxyTLSHandshakeTimeout = 10 * time.Second
	proxyHeaderTimeout       = 45 * time.Second
)

// newProxyTransport connects only to public addresses outside deny (the
// addresses of this host). The address is checked on the socket actually
// being connected, after name resolution, so DNS rebinding cannot reach
// the internal network. No environment proxy is used.
func newProxyTransport(deny []netip.Prefix) http.RoundTripper {
	ok := func(ip netip.Addr) bool { return !netguard.InPrefixes(ip, deny) && netguard.PublicAddr(ip) }
	dialer := &net.Dialer{Timeout: proxyDialTimeout, Control: netguard.Control(ok)}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   proxyTLSHandshakeTimeout,
		ResponseHeaderTimeout: proxyHeaderTimeout,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// newProxyClient never follows redirects itself: OpenProxy checks every
// hop against the endpoint's hosts and adds the secret again.
func newProxyClient(tr http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// isBlockedDial reports a connection refused by the address guard.
func isBlockedDial(err error) bool { return errors.Is(err, netguard.ErrBlocked) }
