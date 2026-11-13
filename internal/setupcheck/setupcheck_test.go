package setupcheck

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/dnscheck"
)

const probeLabel = "check-test"

// fakeResolver answers from a map; unknown names are "not found".
type fakeResolver struct {
	mu    sync.Mutex
	recs  map[string][]string
	fail  map[string]bool
	calls int
}

func newResolver() *fakeResolver {
	return &fakeResolver{recs: map[string][]string{}, fail: map[string]bool{}}
}

func (r *fakeResolver) set(host string, ips ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs[host] = ips
}

func (r *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.fail[host] {
		return nil, &net.DNSError{Err: "server misbehaving", Name: host, IsTemporary: true}
	}
	ips, ok := r.recs[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return out, nil
}

func (r *fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	return host + ".", nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newChecker(r *fakeResolver, rt http.RoundTripper, c *clock) *Checker {
	ch := New(r, rt, c.Now)
	ch.label = func() string { return probeLabel }
	return ch
}

var serverIP = netip.MustParseAddr("203.0.113.10")

func TestDomainStates(t *testing.T) {
	cases := []struct {
		name   string
		apex   []string
		star   []string
		fail   bool
		ips    []netip.Addr
		state  State
		reason Reason
	}{
		{"both point here", []string{"203.0.113.10"}, []string{"203.0.113.10"}, false, []netip.Addr{serverIP}, Done, ReasonNone},
		{"nothing yet", nil, nil, false, []netip.Addr{serverIP}, Todo, ReasonNoRecords},
		{"wildcard missing", []string{"203.0.113.10"}, nil, false, []netip.Addr{serverIP}, Todo, ReasonWildcardMissing},
		{"apex missing", nil, []string{"203.0.113.10"}, false, []netip.Addr{serverIP}, Todo, ReasonApexMissing},
		{"old hosting", []string{"198.51.100.7"}, []string{"203.0.113.10"}, false, []netip.Addr{serverIP}, Problem, ReasonElsewhere},
		{"lookup error", nil, nil, true, []netip.Addr{serverIP}, Problem, ReasonLookupFailed},
		{"addresses unknown", []string{"198.51.100.7"}, []string{"198.51.100.7"}, false, nil, Done, ReasonIPsUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newResolver()
			if tc.apex != nil {
				r.set("sites.test", tc.apex...)
			}
			if tc.star != nil {
				r.set(probeLabel+".sites.test", tc.star...)
			}
			if tc.fail {
				r.fail["sites.test"] = true
			}
			c := newChecker(r, nil, &clock{t: time.Unix(1e9, 0)})
			rep := c.Run(context.Background(), Target{BaseDomain: "sites.test", ServerIPs: tc.ips, APIURL: "https://api.sites.test"}, false)
			if rep.Domain.State != tc.state || rep.Domain.Reason != tc.reason {
				t.Fatalf("domain = %s/%s, want %s/%s", rep.Domain.State, rep.Domain.Reason, tc.state, tc.reason)
			}
			if rep.Domain.Star.Host != probeLabel+".sites.test" {
				t.Errorf("wildcard probe = %q", rep.Domain.Star.Host)
			}
			if tc.state != Done && (rep.HTTPS.State != Todo || rep.HTTPS.Reason != ReasonDNSPending) {
				t.Errorf("https = %s/%s, want todo/dns_pending while DNS is not ready", rep.HTTPS.State, rep.HTTPS.Reason)
			}
		})
	}
}

func TestRecords(t *testing.T) {
	got := Records([]netip.Addr{serverIP, netip.MustParseAddr("2001:db8::10")})
	want := []Record{
		{"@", "A", "203.0.113.10"}, {"@", "AAAA", "2001:db8::10"},
		{"*", "A", "203.0.113.10"}, {"*", "AAAA", "2001:db8::10"},
	}
	if len(got) != len(want) {
		t.Fatalf("records = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %v, want %v", i, got[i], want[i])
		}
	}
	unknown := Records(nil)
	if len(unknown) != 2 || unknown[0].Value != "" || unknown[1].Name != "*" {
		t.Errorf("records without addresses = %v", unknown)
	}
}

// readyResolver resolves the domain and the probe to the server.
func readyResolver() *fakeResolver {
	r := newResolver()
	r.set("sites.test", "203.0.113.10")
	r.set(probeLabel+".sites.test", "203.0.113.10")
	return r
}

func TestHTTPSStates(t *testing.T) {
	ok := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProbePath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"issuer":"x"}`))
	}))
	defer ok.Close()
	broken := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	closed := httptest.NewTLSServer(http.NotFoundHandler())
	closedURL := closed.URL
	closedRT := closed.Client().Transport
	closed.Close()

	cases := []struct {
		name   string
		url    string
		rt     http.RoundTripper
		state  State
		reason Reason
	}{
		{"valid certificate", ok.URL, ok.Client().Transport, Done, ReasonNone},
		{"untrusted certificate", ok.URL, nil, Problem, ReasonCertInvalid},
		{"proxy error", broken.URL, broken.Client().Transport, Problem, ReasonBadStatus},
		{"nothing listens", closedURL, closedRT, Problem, ReasonUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newChecker(readyResolver(), tc.rt, &clock{t: time.Unix(1e9, 0)})
			rep := c.Run(context.Background(), Target{BaseDomain: "sites.test", ServerIPs: []netip.Addr{serverIP}, APIURL: tc.url}, false)
			if rep.HTTPS.State != tc.state || rep.HTTPS.Reason != tc.reason {
				t.Fatalf("https = %s/%s, want %s/%s", rep.HTTPS.State, rep.HTTPS.Reason, tc.state, tc.reason)
			}
			if !strings.HasSuffix(rep.HTTPS.URL, ProbePath) {
				t.Errorf("probe URL = %q", rep.HTTPS.URL)
			}
		})
	}
}

func TestCaching(t *testing.T) {
	r := newResolver()
	clk := &clock{t: time.Unix(1e9, 0)}
	c := newChecker(r, nil, clk)
	tgt := Target{BaseDomain: "sites.test", ServerIPs: []netip.Addr{serverIP}, APIURL: "https://api.sites.test"}
	ctx := context.Background()

	if _, ok := c.Cached(tgt); ok {
		t.Fatal("cache is empty before the first run")
	}
	first := c.Run(ctx, tgt, false)
	if first.Domain.State != Todo {
		t.Fatalf("first run = %s", first.Domain.State)
	}
	calls := r.calls
	r.set("sites.test", "203.0.113.10")
	r.set(probeLabel+".sites.test", "203.0.113.10")

	if got := c.Run(ctx, tgt, false); got.Domain.State != Todo || r.calls != calls {
		t.Fatal("a second run within the TTL must reuse the report")
	}
	if got := c.Run(ctx, tgt, true); got.Domain.State != Todo || r.calls != calls {
		t.Fatal("a forced run within MinInterval must reuse the report")
	}
	clk.advance(MinInterval)
	if got := c.Run(ctx, tgt, true); got.Domain.State != Done {
		t.Fatalf("a forced run after MinInterval = %s, want done", got.Domain.State)
	}
	if got, ok := c.Cached(tgt); !ok || got.Domain.State != Done {
		t.Fatal("Cached must return the last report")
	}
	clk.advance(CacheTTL)
	if _, ok := c.Cached(tgt); ok {
		t.Fatal("the report expires after CacheTTL")
	}
	other := tgt
	other.BaseDomain = "other.test"
	if _, ok := c.Cached(other); ok {
		t.Fatal("a report is not reused for another target")
	}
}

func TestClassifyPrefersWrongAddressOverMissing(t *testing.T) {
	if s, r := classify(dnscheck.NoRecords, dnscheck.PointsElsewhere); s != Problem || r != ReasonElsewhere {
		t.Fatalf("classify = %s/%s", s, r)
	}
}
