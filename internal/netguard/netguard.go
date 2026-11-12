// Package netguard decides which network addresses the server may contact on
// behalf of untrusted input (webhooks, OAuth client metadata, assets fetched
// for a site) and provides dial helpers that enforce the decision on the
// address actually connected to, so DNS rebinding cannot bypass it.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// nonPublic lists special-purpose ranges beyond what netip.Addr's predicates
// cover (private, loopback, link-local, multicast, unspecified).
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 can reach IPv4 internals
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"), // IETF protocol assignments, incl. Teredo
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), // 6to4 embeds IPv4
	netip.MustParsePrefix("fec0::/10"),
}

// globalUnicast6 is the only IPv6 range allocated for global unicast; it
// excludes IPv4-compatible (::/96), site-local and other special ranges.
var globalUnicast6 = netip.MustParsePrefix("2000::/3")

// ErrBlocked is returned when a connection targets a forbidden address.
var ErrBlocked = errors.New("address is not allowed (private or reserved network)")

// InPrefixes reports whether ip (unmapped) is in any of list.
func InPrefixes(ip netip.Addr, list []netip.Prefix) bool {
	ip = ip.Unmap()
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// PublicAddr reports whether a is a public unicast address that the server
// may connect to on behalf of untrusted input.
func PublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	if a.Is6() && !globalUnicast6.Contains(a) {
		return false
	}
	return !InPrefixes(a, nonPublic)
}

// Control returns a net.Dialer Control function that refuses a socket whose
// remote address ok rejects. It runs after name resolution, on the address
// the socket is about to connect to.
func Control(ok func(netip.Addr) bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !ok(ap.Addr()) {
			return ErrBlocked
		}
		return nil
	}
}

// Resolver resolves host names; *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer connects only to addresses ok accepts. It resolves the name itself
// (Resolver may be replaced in tests), refuses the host when any resolved
// address is rejected and connects to the address it checked, so a second
// lookup cannot return something else.
type Dialer struct {
	Resolver Resolver
	OK       func(netip.Addr) bool
	Dialer   *net.Dialer
	// Port, when set, replaces the port being dialled (tests only).
	Port string
}

// DialContext implements http.Transport.DialContext.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if d.Port != "" {
		port = d.Port
	}
	ok := d.OK
	if ok == nil {
		ok = PublicAddr
	}
	var ips []netip.Addr
	if ip, perr := netip.ParseAddr(host); perr == nil {
		ips = []netip.Addr{ip}
	} else {
		res := d.Resolver
		if res == nil {
			res = net.DefaultResolver
		}
		if ips, err = res.LookupNetIP(ctx, "ip", host); err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		if !ok(ip) {
			return nil, ErrBlocked
		}
	}
	nd := d.Dialer
	if nd == nil {
		nd = &net.Dialer{}
	}
	return nd.DialContext(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
}
