package testenv

import (
	"context"
	"net"
	"strings"
	"sync"
)

// ServerIP is the address BASE_DOMAIN and the API host resolve to in tests.
const ServerIP = "203.0.113.10"

// StaticResolver answers DNS lookups from a map, so adapter tests never touch
// the network. Unknown names are "not found". It is safe for concurrent use.
type StaticResolver struct {
	mu      sync.Mutex
	records map[string][]string
}

// NewStaticResolver returns an empty resolver.
func NewStaticResolver() *StaticResolver {
	return &StaticResolver{records: map[string][]string{}}
}

// Set replaces the A/AAAA records of host. A host "*.<domain>" answers for
// every name directly below <domain> that has no records of its own.
func (r *StaticResolver) Set(host string, ips ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[host] = ips
}

// LookupIPAddr implements dnscheck.Resolver.
func (r *StaticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ips, ok := r.records[host]
	if _, parent, dot := strings.Cut(host, "."); !ok && dot {
		ips, ok = r.records["*."+parent]
	}
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return out, nil
}

// LookupCNAME implements dnscheck.Resolver; names have no CNAME.
func (r *StaticResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	if _, err := r.LookupIPAddr(ctx, host); err != nil {
		return "", err
	}
	return host + ".", nil
}
