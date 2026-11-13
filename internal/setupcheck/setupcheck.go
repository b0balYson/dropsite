// Package setupcheck answers the two network questions of the first-run
// checklist: does the domain point at this server, and does the API host
// answer over HTTPS with a valid certificate. Checks are short, run from the
// server itself and are cached briefly, so the interface can ask often.
package setupcheck

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/dnscheck"
)

// Timeouts and cache lifetimes. Both checks together finish within
// DNSTimeout + HTTPSTimeout.
const (
	DNSTimeout   = 3 * time.Second
	HTTPSTimeout = 5 * time.Second
	// CacheTTL is how long a report is reused.
	CacheTTL = time.Minute
	// MinInterval is how soon a forced re-check may run again; earlier
	// requests get the cached report.
	MinInterval = 3 * time.Second
	// ProbePath is fetched on the API host: it is public and always 200.
	ProbePath = "/.well-known/oauth-authorization-server"
)

// State is the status of one checklist step.
type State string

const (
	Done    State = "done"
	Todo    State = "todo"
	Problem State = "problem"
)

// Reason explains a state in a few words; the interface translates it.
type Reason string

const (
	ReasonNone            Reason = ""
	ReasonNoRecords       Reason = "no_records"       // neither name resolves
	ReasonApexMissing     Reason = "apex_missing"     // the domain itself has no records
	ReasonWildcardMissing Reason = "wildcard_missing" // sub-names do not resolve
	ReasonElsewhere       Reason = "points_elsewhere" // a name points at another server
	ReasonLookupFailed    Reason = "lookup_failed"    // DNS error or timeout
	ReasonIPsUnknown      Reason = "ips_unknown"      // resolves, but SERVER_IPS is not set
	ReasonDNSPending      Reason = "dns_pending"      // HTTPS waits for the domain
	ReasonCertInvalid     Reason = "cert_invalid"     // TLS handshake or certificate failed
	ReasonUnreachable     Reason = "unreachable"      // no answer (refused, timeout)
	ReasonBadStatus       Reason = "bad_status"       // answered with a server error
)

// Name is one checked DNS name.
type Name struct {
	Label  string // "@" or "*"
	Host   string // the name actually resolved
	Result dnscheck.Result
}

// Record is a DNS record the administrator creates at the domain provider.
type Record struct {
	Name  string // "@" or "*"
	Type  string // "A" or "AAAA"
	Value string // empty when the server's address is unknown
}

// DomainReport is the outcome of the DNS step.
type DomainReport struct {
	State   State
	Reason  Reason
	Domain  string
	Apex    Name
	Star    Name
	Records []Record
}

// HTTPSReport is the outcome of the HTTPS step.
type HTTPSReport struct {
	State  State
	Reason Reason
	URL    string
	Status int
}

// Report is the result of one run of both checks.
type Report struct {
	Domain    DomainReport
	HTTPS     HTTPSReport
	CheckedAt time.Time
}

// Target describes what to check.
type Target struct {
	BaseDomain string
	// ServerIPs are the server's public addresses (SERVER_IPS); empty when
	// unknown, then any answer counts as long as both names resolve.
	ServerIPs []netip.Addr
	// APIURL is the public https base URL of the API host.
	APIURL string
}

// Checker runs and caches checks. It is safe for concurrent use.
type Checker struct {
	dns    *dnscheck.Checker
	client *http.Client
	now    func() time.Time
	label  func() string

	mu   sync.Mutex // serialises runs, so concurrent pages share one check
	last *Report
	key  string
}

// New returns a checker. A nil resolver means the system resolver, a nil
// transport means a default HTTPS transport and a nil clock means time.Now.
func New(r dnscheck.Resolver, rt http.RoundTripper, now func() time.Time) *Checker {
	if rt == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		rt = tr
	}
	if now == nil {
		now = time.Now
	}
	return &Checker{
		dns: dnscheck.New(r, DNSTimeout),
		client: &http.Client{
			Transport: rt,
			Timeout:   HTTPSTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:   now,
		label: randomLabel,
	}
}

// Cached returns the last report for t if it is still fresh.
func (c *Checker) Cached(t Target) (Report, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil || c.key != t.key() || c.now().Sub(c.last.CheckedAt) >= CacheTTL {
		return Report{}, false
	}
	return *c.last, true
}

// Run returns a fresh enough report: the cached one unless it expired, or,
// with force, unless it is younger than MinInterval.
func (c *Checker) Run(ctx context.Context, t Target, force bool) Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && c.key == t.key() {
		age := c.now().Sub(c.last.CheckedAt)
		if age < MinInterval || (!force && age < CacheTTL) {
			return *c.last
		}
	}
	rep := Report{Domain: c.checkDomain(ctx, t)}
	rep.HTTPS = c.checkHTTPS(ctx, t, rep.Domain.State)
	rep.CheckedAt = c.now()
	c.last, c.key = &rep, t.key()
	return rep
}

func (t Target) key() string {
	k := t.BaseDomain + "|" + t.APIURL
	for _, a := range t.ServerIPs {
		k += "|" + a.String()
	}
	return k
}

// randomLabel is a name nobody has created, so it resolves only through a
// wildcard record.
func randomLabel() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "check-" + hex.EncodeToString(b)
}

func (c *Checker) checkDomain(ctx context.Context, t Target) DomainReport {
	rep := DomainReport{Domain: t.BaseDomain, Records: Records(t.ServerIPs)}
	rep.Apex = Name{Label: "@", Host: t.BaseDomain}
	rep.Star = Name{Label: "*", Host: c.label() + "." + t.BaseDomain}
	var wg sync.WaitGroup
	for _, n := range []*Name{&rep.Apex, &rep.Star} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Result = c.dns.Check(ctx, n.Host, t.ServerIPs)
		}()
	}
	wg.Wait()
	rep.State, rep.Reason = classify(rep.Apex.Result.Verdict, rep.Star.Result.Verdict)
	return rep
}

// classify turns the verdicts of the domain and the wildcard probe into a
// step state. A wrong address beats a missing one: it needs fixing, not
// waiting.
func classify(apex, star dnscheck.Verdict) (State, Reason) {
	switch {
	case apex == dnscheck.PointsElsewhere || star == dnscheck.PointsElsewhere:
		return Problem, ReasonElsewhere
	case apex == dnscheck.Failed || star == dnscheck.Failed:
		return Problem, ReasonLookupFailed
	case apex == dnscheck.NoRecords && star == dnscheck.NoRecords:
		return Todo, ReasonNoRecords
	case apex == dnscheck.NoRecords:
		return Todo, ReasonApexMissing
	case star == dnscheck.NoRecords:
		return Todo, ReasonWildcardMissing
	case apex == dnscheck.Unknown || star == dnscheck.Unknown:
		return Done, ReasonIPsUnknown
	}
	return Done, ReasonNone
}

// Records lists the A/AAAA records for the domain and the wildcard. Without
// known addresses it returns one A record per name with an empty value.
func Records(ips []netip.Addr) []Record {
	var v4, v6 []string
	for _, a := range ips {
		if a.Is4() {
			v4 = append(v4, a.String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	if len(v4)+len(v6) == 0 {
		return []Record{{Name: "@", Type: "A"}, {Name: "*", Type: "A"}}
	}
	var out []Record
	for _, name := range []string{"@", "*"} {
		for _, v := range v4 {
			out = append(out, Record{Name: name, Type: "A", Value: v})
		}
		for _, v := range v6 {
			out = append(out, Record{Name: name, Type: "AAAA", Value: v})
		}
	}
	return out
}

func (c *Checker) checkHTTPS(ctx context.Context, t Target, domain State) HTTPSReport {
	rep := HTTPSReport{URL: t.APIURL + ProbePath}
	if domain != Done {
		rep.State, rep.Reason = Todo, ReasonDNSPending
		return rep
	}
	ctx, cancel := context.WithTimeout(ctx, HTTPSTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rep.URL, nil)
	if err != nil {
		rep.State, rep.Reason = Problem, ReasonUnreachable
		return rep
	}
	resp, err := c.client.Do(req)
	if err != nil {
		rep.State = Problem
		rep.Reason = ReasonUnreachable
		if isCertError(err) {
			rep.Reason = ReasonCertInvalid
		}
		return rep
	}
	_ = resp.Body.Close()
	rep.Status = resp.StatusCode
	if resp.StatusCode >= http.StatusInternalServerError {
		rep.State, rep.Reason = Problem, ReasonBadStatus
		return rep
	}
	rep.State = Done
	return rep
}

// isCertError reports a failed TLS handshake or certificate verification:
// an untrusted, expired or mismatched certificate, or none issued yet (the
// proxy then aborts the handshake with an alert).
func isCertError(err error) bool {
	var (
		verr  *tls.CertificateVerificationError
		uaerr x509.UnknownAuthorityError
		herr  x509.HostnameError
		cierr x509.CertificateInvalidError
		alert tls.AlertError
		rerr  tls.RecordHeaderError
	)
	if errors.As(err, &verr) || errors.As(err, &uaerr) || errors.As(err, &herr) ||
		errors.As(err, &cierr) || errors.As(err, &alert) || errors.As(err, &rerr) {
		return true
	}
	// A TLS alert from the peer arrives as an OpError with this Op.
	var oerr *net.OpError
	return errors.As(err, &oerr) && oerr.Op == "remote error"
}
