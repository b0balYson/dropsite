package service

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/dnscheck"
	"github.com/b0balYson/dropsite/internal/domain"
)

// stubResolver answers from a map and counts lookups.
type stubResolver struct {
	ips   map[string][]string
	calls atomic.Int32
}

func (r *stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.calls.Add(1)
	list, ok := r.ips[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := []net.IPAddr{}
	for _, s := range list {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func (r *stubResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	return host + ".", nil
}

func (e *env) linked(host string) bool {
	e.t.Helper()
	_, err := os.Lstat(filepath.Join(e.root, "by-host", host))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	return err == nil
}

func TestDomainOwnerFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	other := e.user("bob@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "promo-may")
	if _, err := e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "v1"}), domain.SourceUI); err != nil {
		t.Fatal(err)
	}

	h, err := e.svc.AddDomain(ctx, u, "promo-may", "  Promo.Example.RU. ")
	if err != nil {
		t.Fatal(err)
	}
	if h.Hostname != "promo.example.ru" || h.Kind != domain.HostExternal || h.Status() != domain.HostPending {
		t.Fatalf("added %+v", h)
	}
	if e.linked("promo.example.ru") {
		t.Fatal("pending domain must not be published")
	}
	v, _ := e.svc.GetSite(ctx, u, site.Site.ID)
	if len(v.URLs) != 2 || len(v.Hostnames) != 3 || slices.Contains(v.URLs, "https://promo.example.ru/") {
		t.Fatalf("pending domain must not be a site URL: %v", v.URLs)
	}
	hosts, err := e.svc.ListHostnames(ctx, u, "promo-may")
	if err != nil || len(hosts) != 3 {
		t.Fatalf("hostnames %+v %v", hosts, err)
	}

	// Another user: same "not found" for adding, listing and removing.
	_, err = e.svc.AddDomain(ctx, other, site.Site.ID, "x.example.ru")
	wantNotFound(t, err)
	_, err = e.svc.ListHostnames(ctx, other, site.Site.ID)
	wantNotFound(t, err)
	wantNotFound(t, e.svc.RemoveDomain(ctx, other, site.Site.ID, "promo.example.ru"))

	// Taken in any status, without naming the holder.
	otherSite, _ := e.svc.CreateSite(ctx, other, "Other", "")
	_, err = e.svc.AddDomain(ctx, other, otherSite.Site.ID, "PROMO.example.ru")
	wantCode(t, err, domain.CodeDomainTaken)
	if de := domain.AsError(err); de.Params["domain"] != "promo.example.ru" || de.Params["site"] != nil {
		t.Fatalf("params %+v", de.Params)
	}

	// Validation.
	for raw, code := range map[string]string{
		"promo.pp.test": domain.CodeDomainForbidden, "pp.test": domain.CodeDomainForbidden,
		"api.pp.test": domain.CodeDomainForbidden, "203.0.113.7": domain.CodeDomainInvalid,
		"localhost": domain.CodeDomainInvalid, "https://x.example.ru": domain.CodeDomainInvalid,
	} {
		_, err := e.svc.AddDomain(ctx, u, site.Site.ID, raw)
		wantCode(t, err, code)
	}

	// Default and slug addresses cannot be removed through the domain call.
	wantNotFound(t, e.svc.RemoveDomain(ctx, u, site.Site.ID, "promo-may.pp.test"))
	wantNotFound(t, e.svc.RemoveDomain(ctx, u, site.Site.ID, site.Site.ID+".pp.test"))
	wantNotFound(t, e.svc.RemoveDomain(ctx, u, site.Site.ID, "not a host"))

	if err := e.svc.RemoveDomain(ctx, u, site.Site.ID, "Promo.Example.ru"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddDomain(ctx, other, otherSite.Site.ID, "promo.example.ru"); err != nil {
		t.Fatalf("released domain must be free: %v", err)
	}
	if got := e.auditMeta("domain.add"); len(got) != 2 || got[1]["domain"] != "promo.example.ru" {
		t.Fatalf("audit add %+v", got)
	}
	if got := e.auditMeta("domain.remove"); len(got) != 1 {
		t.Fatalf("audit remove %+v", got)
	}
}

func TestDomainLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.MaxDomainsPerSite = 2 })
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	for _, h := range []string{"a.example.ru", "b.example.ru"} {
		if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.svc.AddDomain(ctx, u, site.Site.ID, "c.example.ru")
	wantCode(t, err, domain.CodeDomainLimit)
	if de := domain.AsError(err); de.Params["limit"] != 2 {
		t.Fatalf("params %+v", de.Params)
	}
}

func TestDomainReview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	if _, err := e.svc.Deploy(ctx, u, site.Site.ID, zipOf(t, map[string]string{"index.html": "v1"}), domain.SourceUI); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"promo.example.ru", "example.ru"} {
		if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, h); err != nil {
			t.Fatal(err)
		}
	}

	// Users cannot review or list, not even their own domains.
	_, err := e.svc.ApproveDomain(ctx, u, "promo.example.ru")
	wantNotFound(t, err)
	_, err = e.svc.RejectDomain(ctx, u, "promo.example.ru", "x")
	wantNotFound(t, err)
	_, _, err = e.svc.ListDomains(ctx, u, DomainFilter{})
	wantNotFound(t, err)
	_, err = e.svc.CheckDomainDNS(ctx, u, "promo.example.ru")
	wantNotFound(t, err)

	rows, total, err := e.svc.ListDomains(ctx, e.admin, DomainFilter{Status: domain.HostPending})
	if err != nil || total != 2 || rows[0].OwnerEmail != "ann@pp.test" || rows[0].SiteTitle != "Promo" {
		t.Fatalf("pending %+v %d %v", rows, total, err)
	}

	d, err := e.svc.ApproveDomain(ctx, e.admin, "Promo.Example.RU")
	if err != nil || d.Hostname.Status() != domain.HostActive || d.OwnerEmail != "ann@pp.test" {
		t.Fatalf("approve %+v %v", d, err)
	}
	if body, err := e.served("promo.example.ru", "index.html"); err != nil || body != "v1" {
		t.Fatalf("approved domain must serve the site: %q %v", body, err)
	}
	if _, err := e.svc.ApproveDomain(ctx, e.admin, "example.ru"); err != nil {
		t.Fatal(err)
	}
	if body, err := e.served("example.ru", "index.html"); err != nil || body != "v1" {
		t.Fatalf("apex of an external domain: %q %v", body, err)
	}
	v, _ := e.svc.GetSite(ctx, u, site.Site.ID)
	if !slices.Contains(v.URLs, "https://promo.example.ru/") || v.URL != "https://"+site.Site.ID+".pp.test/" {
		t.Fatalf("urls %v", v.URLs)
	}

	_, err = e.svc.RejectDomain(ctx, e.admin, "promo.example.ru", "  ")
	wantCode(t, err, domain.CodeDomainReasonInvalid)
	d, err = e.svc.RejectDomain(ctx, e.admin, "promo.example.ru", "not ours")
	if err != nil || d.Hostname.Status() != domain.HostRejected || d.Hostname.RejectReason != "not ours" {
		t.Fatalf("reject %+v %v", d, err)
	}
	if e.linked("promo.example.ru") {
		t.Fatal("rejected domain must be unpublished at once")
	}
	_, err = e.svc.ApproveDomain(ctx, e.admin, "missing.example.ru")
	wantNotFound(t, err)
	_, err = e.svc.ApproveDomain(ctx, e.admin, site.Site.ID+".pp.test")
	wantNotFound(t, err)

	if got := e.auditMeta("domain.reject"); len(got) != 1 || got[0]["reason"] != "not ours" || got[0]["domain"] != "promo.example.ru" {
		t.Fatalf("audit reject %+v", got)
	}
	if got := e.auditMeta("domain.approve"); len(got) != 2 {
		t.Fatalf("audit approve %+v", got)
	}

	// Removing an approved domain unpublishes it.
	if err := e.svc.RemoveDomain(ctx, u, site.Site.ID, "example.ru"); err != nil {
		t.Fatal(err)
	}
	if e.linked("example.ru") {
		t.Fatal("removed domain still published")
	}
}

func TestDeleteSiteReleasesDomains(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, "promo.example.ru"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApproveDomain(ctx, e.admin, "promo.example.ru"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteSite(ctx, u, site.Site.ID); err != nil {
		t.Fatal(err)
	}
	if e.linked("promo.example.ru") {
		t.Fatal("deleted site's domain still published")
	}
	_, err := e.svc.ApproveDomain(ctx, e.admin, "promo.example.ru")
	wantNotFound(t, err)
	next, _ := e.svc.CreateSite(ctx, e.user("bob@pp.test"), "Next", "")
	if _, err := e.svc.AddDomain(ctx, e.admin, next.Site.ID, "promo.example.ru"); err != nil {
		t.Fatalf("admin adds a released domain to any site: %v", err)
	}
}

// A restart reconciles by-host from the database: a stale link of a pending
// domain disappears, an approved one is restored.
func TestDomainReconcile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	for _, h := range []string{"ok.example.ru", "wait.example.ru"} {
		if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.ApproveDomain(ctx, e.admin, "ok.example.ru"); err != nil {
		t.Fatal(err)
	}
	by := filepath.Join(e.root, "by-host")
	if err := os.Remove(filepath.Join(by, "ok.example.ru")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sites/"+site.Site.ID+"/current", filepath.Join(by, "wait.example.ru")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.linked("ok.example.ru") || e.linked("wait.example.ru") {
		t.Fatal("reconcile must follow approvals")
	}
}

func TestDomainDNSCheck(t *testing.T) {
	r := &stubResolver{ips: map[string][]string{
		"pp.test":          {"203.0.113.7"},
		"api.pp.test":      {"203.0.113.7"},
		"promo.example.ru": {"203.0.113.7"},
		"bad.example.ru":   {"198.51.100.1"},
	}}
	e := newEnv(t)
	e.svc.dns = dnscheck.New(r, 0)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	for _, h := range []string{"promo.example.ru", "bad.example.ru", "none.example.ru"} {
		if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]dnscheck.Verdict{
		"promo.example.ru": dnscheck.PointsHere, "bad.example.ru": dnscheck.PointsElsewhere, "none.example.ru": dnscheck.NoRecords,
	}
	for h, verdict := range want {
		res, err := e.svc.CheckDomainDNS(ctx, e.admin, h)
		if err != nil || res.Verdict != verdict {
			t.Fatalf("%s: %+v %v", h, res, err)
		}
	}
	// Server addresses are resolved once and cached: 2 for discovery + 3 checks.
	if n := r.calls.Load(); n != 5 {
		t.Fatalf("lookups %d", n)
	}
	// Only known external domains are looked up.
	_, err := e.svc.CheckDomainDNS(ctx, e.admin, "random.example.org")
	wantNotFound(t, err)
	_, err = e.svc.CheckDomainDNS(ctx, e.admin, site.Site.ID+".pp.test")
	wantNotFound(t, err)

	setup, err := e.svc.DomainSetup(ctx, u, site.Site.ID)
	if err != nil || !slices.Equal(setup.ServerIPs, []netip.Addr{netip.MustParseAddr("203.0.113.7")}) ||
		setup.CNAMETarget != site.Site.ID+".pp.test" || setup.MaxDomains != 5 {
		t.Fatalf("setup %+v %v", setup, err)
	}
	_, err = e.svc.DomainSetup(ctx, e.user("bob@pp.test"), site.Site.ID)
	wantNotFound(t, err)
}

func TestDomainServerIPsFromConfig(t *testing.T) {
	r := &stubResolver{ips: map[string][]string{"promo.example.ru": {"2001:db8::7"}}}
	e := newEnv(t, func(c *config.Config) { c.ServerIPs = []netip.Addr{netip.MustParseAddr("2001:db8::7")} })
	e.svc.dns = dnscheck.New(r, 0)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, "promo.example.ru"); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.CheckDomainDNS(ctx, e.admin, "promo.example.ru")
	if err != nil || res.Verdict != dnscheck.PointsHere || r.calls.Load() != 1 {
		t.Fatalf("%+v %v calls=%d", res, err, r.calls.Load())
	}
}

// Without resolvable server addresses the check still answers.
func TestDomainDNSUnknownServer(t *testing.T) {
	r := &stubResolver{ips: map[string][]string{"promo.example.ru": {"203.0.113.7"}}}
	e := newEnv(t)
	e.svc.dns = dnscheck.New(r, 0)
	ctx := context.Background()
	u := e.user("ann@pp.test")
	site, _ := e.svc.CreateSite(ctx, u, "Promo", "")
	if _, err := e.svc.AddDomain(ctx, u, site.Site.ID, "promo.example.ru"); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.CheckDomainDNS(ctx, e.admin, "promo.example.ru")
	if err != nil || res.Verdict != dnscheck.Unknown {
		t.Fatalf("%+v %v", res, err)
	}
	setup, err := e.svc.DomainSetup(ctx, u, site.Site.ID)
	if err != nil || len(setup.ServerIPs) != 0 {
		t.Fatalf("%+v %v", setup, err)
	}
}
