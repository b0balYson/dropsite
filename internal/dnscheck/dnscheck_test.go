package dnscheck

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"
)

type fakeResolver struct {
	ips   map[string][]string
	cname map[string]string
	fail  map[string]error
	block bool
}

func (f *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := f.fail[host]; err != nil {
		return nil, err
	}
	list, ok := f.ips[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, 0, len(list))
	for _, s := range list {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func (f *fakeResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if c, ok := f.cname[host]; ok {
		return c, nil
	}
	if _, ok := f.ips[host]; ok {
		return host + ".", nil // Go returns the name itself when there is no CNAME
	}
	return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestCheck(t *testing.T) {
	r := &fakeResolver{
		ips: map[string][]string{
			"here.example.ru":      {"203.0.113.7", "2001:db8::7"},
			"alias.example.ru":     {"203.0.113.7"},
			"elsewhere.example.ru": {"198.51.100.1", "203.0.113.7"},
			"mapped.example.ru":    {"::ffff:203.0.113.7"},
		},
		cname: map[string]string{"alias.example.ru": "AAAAAAAAAA.Example.com."},
		fail:  map[string]error{"broken.example.ru": &net.DNSError{Err: "server misbehaving", Name: "broken.example.ru", IsTemporary: true}},
	}
	c := New(r, time.Second)
	expected := addrs("203.0.113.7", "2001:db8::7")
	cases := []struct {
		host    string
		verdict Verdict
		addrs   []netip.Addr
		cname   string
	}{
		{"here.example.ru", PointsHere, addrs("203.0.113.7", "2001:db8::7"), ""},
		{"alias.example.ru", PointsHere, addrs("203.0.113.7"), "aaaaaaaaaa.example.com"},
		{"mapped.example.ru", PointsHere, addrs("203.0.113.7"), ""},
		{"elsewhere.example.ru", PointsElsewhere, addrs("198.51.100.1", "203.0.113.7"), ""},
		{"missing.example.ru", NoRecords, nil, ""},
		{"broken.example.ru", Failed, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			res := c.Check(context.Background(), tc.host, expected)
			if res.Verdict != tc.verdict || !slices.Equal(res.Addresses, tc.addrs) || res.CNAME != tc.cname {
				t.Fatalf("got %+v", res)
			}
			if res.Host != tc.host || !slices.Equal(res.Expected, expected) {
				t.Fatalf("echo %+v", res)
			}
		})
	}

	if res := c.Check(context.Background(), "here.example.ru", nil); res.Verdict != Unknown {
		t.Fatalf("no expected addresses: %+v", res)
	}
}

func TestCheckTimeout(t *testing.T) {
	c := New(&fakeResolver{block: true}, 50*time.Millisecond)
	start := time.Now()
	res := c.Check(context.Background(), "slow.example.ru", addrs("203.0.113.7"))
	if res.Verdict != Failed {
		t.Fatalf("got %+v", res)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("check took %v", d)
	}
}

func TestAddresses(t *testing.T) {
	r := &fakeResolver{ips: map[string][]string{
		"example.com":     {"203.0.113.7", "2001:db8::7"},
		"api.example.com": {"203.0.113.7", "203.0.113.8"},
	}}
	c := New(r, time.Second)
	got, err := c.Addresses(context.Background(), "example.com", "api.example.com", "missing.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, addrs("203.0.113.7", "203.0.113.8", "2001:db8::7")) {
		t.Fatalf("got %v", got)
	}
	if _, err := c.Addresses(context.Background(), "missing.example.com"); err == nil {
		t.Fatal("no addresses must be an error")
	}
	if _, err := New(&fakeResolver{block: true}, 20*time.Millisecond).Addresses(context.Background(), "example.com"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
}

func TestDefaults(t *testing.T) {
	c := New(nil, 0)
	if c.timeout != DefaultTimeout || c.r == nil {
		t.Fatalf("defaults %+v", c)
	}
}
