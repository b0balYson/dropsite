// Package dnscheck tells an administrator whether an external domain points
// at this server. It only resolves names; it never connects to them. The
// result is informational and never blocks an approval.
package dnscheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// DefaultTimeout bounds one check (all lookups together).
const DefaultTimeout = 5 * time.Second

// Resolver is the part of *net.Resolver the checker uses; tests substitute it.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// Verdict summarises a check.
type Verdict string

const (
	PointsHere      Verdict = "points_here"      // every address is one of the server's
	PointsElsewhere Verdict = "points_elsewhere" // at least one address is not the server's
	NoRecords       Verdict = "no_records"       // the name has no A/AAAA records
	Failed          Verdict = "failed"           // lookup error or timeout
	Unknown         Verdict = "unknown"          // resolved, but the server's addresses are unknown
)

// Result is the outcome of a check.
type Result struct {
	Host      string
	Addresses []netip.Addr // A/AAAA after following CNAMEs, sorted
	CNAME     string       // canonical name if it differs from Host, lower case, no trailing dot
	Expected  []netip.Addr
	Verdict   Verdict
}

// Checker resolves names with a time limit.
type Checker struct {
	r       Resolver
	timeout time.Duration
}

// New returns a checker; a nil resolver means net.DefaultResolver and a
// non-positive timeout means DefaultTimeout.
func New(r Resolver, timeout time.Duration) *Checker {
	if r == nil {
		r = net.DefaultResolver
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Checker{r: r, timeout: timeout}
}

// Check resolves host and compares its addresses with expected.
func (c *Checker) Check(ctx context.Context, host string, expected []netip.Addr) Result {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	res := Result{Host: host, Expected: expected}
	ips, err := c.r.LookupIPAddr(ctx, host)
	switch {
	case isNotFound(err):
		res.Verdict = NoRecords
		return res
	case err != nil:
		res.Verdict = Failed
		return res
	}
	res.Addresses = toAddrs(ips)
	if cname, err := c.r.LookupCNAME(ctx, host); err == nil {
		cname = strings.TrimSuffix(strings.ToLower(cname), ".")
		if cname != host {
			res.CNAME = cname
		}
	}
	res.Verdict = classify(res.Addresses, expected)
	return res
}

func classify(found, expected []netip.Addr) Verdict {
	switch {
	case len(found) == 0:
		return NoRecords
	case len(expected) == 0:
		return Unknown
	}
	for _, a := range found {
		if !slices.Contains(expected, a) {
			return PointsElsewhere
		}
	}
	return PointsHere
}

// Addresses returns the sorted union of the A/AAAA records of hosts, for
// discovering the server's own addresses. Names that do not resolve are
// skipped; an error is returned only when nothing resolved.
func (c *Checker) Addresses(ctx context.Context, hosts ...string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var out []netip.Addr
	var errs []error
	for _, h := range hosts {
		ips, err := c.r.LookupIPAddr(ctx, h)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, toAddrs(ips)...)
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	out = slices.Compact(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("resolve server addresses: %w", errors.Join(errs...))
	}
	return out, nil
}

func toAddrs(ips []net.IPAddr) []netip.Addr {
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip.IP); ok {
			out = append(out, a.Unmap())
		}
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return slices.Compact(out)
}

func isNotFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}
