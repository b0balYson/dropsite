// Package guard holds the abuse-protection building blocks that do not need
// the database: the network key of a client address, the periodicity
// detector, suspicion levels, rate-limit violation counting, the marker
// files Caddy checks and the access-log lines the ban detector reads.
// internal/service wires them together and keeps the source of truth.
package guard

import (
	"net/netip"
	"strings"
)

// IPv6NetBits is the prefix length an IPv6 client is identified by: one
// subscriber usually holds a whole /64 and can rotate addresses inside it.
const IPv6NetBits = 64

// NetKey returns the key a client address is limited and banned by: the
// IPv4 address itself, or the IPv6 /64 network in CIDR form. An empty or
// unparsable address yields "".
func NetKey(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(IPv6NetBits)
	if err != nil {
		return ""
	}
	return p.String()
}

// CanonicalIP returns the address in the form Caddy prints {client_ip}
// (RFC 5952 for IPv6, dotted quad for IPv4), or "" when ip is not an address.
func CanonicalIP(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	return a.Unmap().WithZone("").String()
}

// IsNet reports whether key is an IPv6 network key (as opposed to a single
// IPv4 address).
func IsNet(key string) bool { return strings.Contains(key, "/") }
