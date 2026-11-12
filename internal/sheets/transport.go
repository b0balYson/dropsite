package sheets

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/netguard"
)

// Limits of one call to Google.
const (
	CallTimeout      = 20 * time.Second
	MaxResponseBytes = 10 << 20
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 10 * time.Second
)

// ErrHostNotAllowed is returned for a request outside the two Google hosts.
var ErrHostNotAllowed = errors.New("request to a host other than the Google Sheets API refused")

// NewTransport connects only to public addresses outside deny (the
// addresses of this host), checked on the socket being connected, without
// an environment proxy.
func NewTransport(deny []netip.Prefix) http.RoundTripper {
	ok := func(ip netip.Addr) bool { return !netguard.InPrefixes(ip, deny) && netguard.PublicAddr(ip) }
	dialer := &net.Dialer{Timeout: dialTimeout, Control: netguard.Control(ok)}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   handshakeTimeout,
		ResponseHeaderTimeout: CallTimeout,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// guard lets through only https requests to the two Google hosts on the
// default port; it wraps every transport, the test ones included.
type guard struct{ next http.RoundTripper }

func (g guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if !allowedURL(r) {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, ErrHostNotAllowed
	}
	return g.next.RoundTrip(r)
}

func allowedURL(r *http.Request) bool {
	u := r.URL
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == TokenHost || h == APIHost
}

// newHTTPClient never follows redirects.
func newHTTPClient(tr http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: guard{next: tr},
		Timeout:   CallTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
