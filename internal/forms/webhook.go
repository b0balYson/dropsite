package forms

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/b0balYson/dropsite/internal/netguard"
)

// SignatureHeader carries the HMAC-SHA256 of the webhook body.
const SignatureHeader = "X-Dropsite-Signature"

const maxWebhookURLLen = 2048

// errBlockedAddress is returned when a connection targets a forbidden address.
var errBlockedAddress = errors.New("webhook address is not allowed (private or reserved network)")

func inPrefixes(ip netip.Addr, list []netip.Prefix) bool { return netguard.InPrefixes(ip, list) }

// PublicAddr reports whether ip may be contacted by a webhook delivery, or
// is in allow (FORM_WEBHOOK_ALLOW_CIDRS).
func PublicAddr(ip netip.Addr, allow []netip.Prefix) bool {
	return inPrefixes(ip, allow) || netguard.PublicAddr(ip)
}

// ValidateWebhookURL checks a URL when a channel is created. Plain http is
// accepted only when allow is non-empty (test networks); the address is
// checked again on every connection.
func ValidateWebhookURL(raw string, allow []netip.Prefix) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxWebhookURLLen {
		return "", errors.New("enter an https:// address")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" {
		return "", errors.New("enter a full https:// address")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && len(allow) > 0:
	default:
		return "", errors.New("the address must start with https://")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return "", errors.New("the address must not contain spaces")
	}
	if ip, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil && !PublicAddr(ip, allow) {
		return "", errBlockedAddress
	}
	if host := strings.ToLower(u.Hostname()); host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if len(allow) == 0 {
			return "", errBlockedAddress
		}
	}
	u.Fragment = ""
	return u.String(), nil
}

// Webhook timeouts: a slow receiver holds a delivery slot for at most
// DefaultWebhookTimeout.
const (
	DefaultWebhookTimeout = 10 * time.Second
	webhookDialTimeout    = 3 * time.Second
	webhookHeaderTimeout  = 8 * time.Second
)

// WebhookOptions configure the webhook client. Deny lists networks that are
// refused even when Allow covers them: the addresses of the control host
// itself, so an allowlisted private network never exposes its internal
// listener.
type WebhookOptions struct {
	Allow   []netip.Prefix
	Deny    []netip.Prefix
	Timeout time.Duration
	RootCAs *x509.CertPool // tests
}

// SelfPrefixes returns the networks that mean "this host": loopback and
// unspecified ranges and every address of the local interfaces.
func SelfPrefixes(addrs []net.Addr) []netip.Prefix {
	out := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("::/128"),
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(n.IP); ok {
			ip = ip.Unmap()
			out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
		}
	}
	return out
}

// WebhookClient posts signed submissions with SSRF protection: every
// connection's resolved address is checked (DNS rebinding cannot bypass it),
// redirects are never followed and no proxy is used.
type WebhookClient struct {
	https *http.Client
	plain *http.Client // only for addresses in Allow
}

// NewWebhookClient builds the client.
func NewWebhookClient(o WebhookOptions) *WebhookClient {
	if o.Timeout <= 0 {
		o.Timeout = DefaultWebhookTimeout
	}
	allow := append([]netip.Prefix(nil), o.Allow...)
	deny := append([]netip.Prefix(nil), o.Deny...)
	public := func(ip netip.Addr) bool { return !inPrefixes(ip.Unmap(), deny) && PublicAddr(ip, allow) }
	allowedOnly := func(ip netip.Addr) bool { return !inPrefixes(ip.Unmap(), deny) && inPrefixes(ip.Unmap(), allow) }
	return &WebhookClient{
		https: newGuardedClient(public, o),
		plain: newGuardedClient(allowedOnly, o),
	}
}

func newGuardedClient(ok func(netip.Addr) bool, o WebhookOptions) *http.Client {
	dialer := &net.Dialer{
		Timeout: webhookDialTimeout,
		Control: func(network, address string, c syscall.RawConn) error {
			if netguard.Control(ok)(network, address, c) != nil {
				return errBlockedAddress
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: webhookHeaderTimeout,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   o.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Sign returns the signature header value for body.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hexOf(m.Sum(nil))
}

// Post delivers body to target with the signature header.
func (c *WebhookClient) Post(ctx context.Context, target, secret string, body []byte) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Permanent("webhook: invalid address")
	}
	client := c.https
	if u.Scheme == "http" {
		client = c.plain
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return Permanent("webhook: invalid address")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dropsite-forms")
	req.Header.Set(SignatureHeader, Sign(secret, body))
	resp, err := client.Do(req) //nolint:gosec // SSRF guarded by the dialer
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			return Permanent("webhook: " + errBlockedAddress.Error())
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Temporary("webhook: request failed: " + err.Error())
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return Permanent(fmt.Sprintf("webhook: HTTP %d (redirects are not followed)", resp.StatusCode))
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return Temporary(fmt.Sprintf("webhook: HTTP %d", resp.StatusCode))
	default:
		return Permanent(fmt.Sprintf("webhook: HTTP %d", resp.StatusCode))
	}
}
