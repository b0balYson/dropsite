package oauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

// Client metadata document fetching limits (spec oauth).
const (
	cimdTimeout    = 5 * time.Second
	cimdMaxBytes   = 64 << 10
	cimdDefaultTTL = 10 * time.Minute
	cimdMaxTTL     = time.Hour
	cimdCacheSize  = 512
)

// Resolver resolves host names; *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// nonPublic lists ranges that are not reachable public unicast addresses
// beyond what netip already classifies (private, loopback, link-local,
// multicast, unspecified).
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 can reach IPv4 internals
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), // 6to4 embeds IPv4
	netip.MustParsePrefix("2001::/32"), // Teredo
	netip.MustParsePrefix("100::/64"),
}

// PublicAddr reports whether a is a public unicast address that a server may
// connect to on behalf of an untrusted client.
func PublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var errNotPublic = errors.New("the client metadata host resolves to a non-public address")

// fetcher loads client metadata documents with SSRF protection: only https,
// every resolved address must be public, the connection goes to the address
// that was checked (no second lookup), no redirects, a timeout and a size cap.
type fetcher struct {
	client *http.Client
	allow  func(netip.Addr) bool

	mu    sync.Mutex
	cache map[string]cimdEntry
	now   func() time.Time
}

type cimdEntry struct {
	client  Client
	expires time.Time
}

func newFetcher(res Resolver, tlsCfg *tls.Config, allow func(netip.Addr) bool) *fetcher {
	if res == nil {
		res = net.DefaultResolver
	}
	if allow == nil {
		allow = PublicAddr
	}
	f := &fetcher{allow: allow, cache: map[string]cimdEntry{}, now: time.Now}
	dialer := &net.Dialer{Timeout: cimdTimeout}
	tr := &http.Transport{
		Proxy: nil, // a proxy would bypass the address check
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := res.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("resolve %s: no addresses", host)
			}
			for _, ip := range ips {
				if !f.allow(ip) {
					return nil, errNotPublic
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
		},
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   cimdTimeout,
		ResponseHeaderTimeout: cimdTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		DisableCompression:    true,
	}
	f.client = &http.Client{
		Transport: tr,
		Timeout:   cimdTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return f
}

// IsMetadataURL reports whether a client_id is a client metadata document URL.
func IsMetadataURL(clientID string) bool {
	u, err := url.Parse(clientID)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Path != "" && u.Path != "/" &&
		u.User == nil && u.Fragment == "" && !strings.Contains(u.Path, "/../") && !strings.HasSuffix(u.Path, "/..")
}

type metadataDoc struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// get returns the client described by the document at clientID.
func (f *fetcher) get(ctx context.Context, clientID string) (Client, error) {
	now := f.now()
	f.mu.Lock()
	if e, ok := f.cache[clientID]; ok && now.Before(e.expires) {
		f.mu.Unlock()
		return e.client, nil
	}
	f.mu.Unlock()
	c, ttl, err := f.fetch(ctx, clientID)
	if err != nil {
		return Client{}, err
	}
	f.mu.Lock()
	if len(f.cache) >= cimdCacheSize {
		f.cache = map[string]cimdEntry{}
	}
	f.cache[clientID] = cimdEntry{client: c, expires: now.Add(ttl)}
	f.mu.Unlock()
	return c, nil
}

func (f *fetcher) fetch(ctx context.Context, clientID string) (Client, time.Duration, error) {
	if !IsMetadataURL(clientID) {
		return Client{}, 0, errors.New("client_id is not an https URL with a path")
	}
	ctx, cancel := context.WithTimeout(ctx, cimdTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return Client{}, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req) //nolint:gosec // G704: the dialer only connects to verified public addresses
	if err != nil {
		return Client{}, 0, fmt.Errorf("fetch client metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Client{}, 0, fmt.Errorf("fetch client metadata: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBytes+1))
	if err != nil {
		return Client{}, 0, fmt.Errorf("read client metadata: %w", err)
	}
	if len(body) > cimdMaxBytes {
		return Client{}, 0, errors.New("client metadata document is larger than 64 KB")
	}
	var doc metadataDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return Client{}, 0, fmt.Errorf("client metadata is not valid JSON: %w", err)
	}
	if doc.ClientID != clientID {
		return Client{}, 0, errors.New("client_id in the metadata document does not match its URL")
	}
	if len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > service.MaxRedirectURIs {
		return Client{}, 0, errors.New("client metadata must list redirect_uris")
	}
	for _, u := range doc.RedirectURIs {
		if !service.ValidRedirectURI(u) {
			return Client{}, 0, errors.New("client metadata lists a redirect_uri that is neither https nor loopback")
		}
	}
	name := doc.ClientName
	if strings.TrimSpace(name) == "" {
		name = req.URL.Host
	}
	return Client{ID: clientID, Name: name, RedirectURIs: doc.RedirectURIs, Metadata: true}, cacheTTL(resp.Header), nil
}

// cacheTTL honours Cache-Control max-age within bounds; no-store disables caching.
func cacheTTL(h http.Header) time.Duration {
	cc := strings.ToLower(h.Get("Cache-Control"))
	if strings.Contains(cc, "no-store") || strings.Contains(cc, "no-cache") {
		return 0
	}
	for _, part := range strings.Split(cc, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "max-age="); ok {
			if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
				return min(time.Duration(secs)*time.Second, cimdMaxTTL)
			}
		}
	}
	return cimdDefaultTTL
}
