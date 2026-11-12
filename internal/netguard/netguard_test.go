package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.5": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "192.0.2.1": false,
		"198.18.0.1": false, "203.0.113.9": false, "240.0.0.1": false, "255.255.255.255": false,
		"::1": false, "::": false, "fe80::1": false, "fc00::1": false, "fec0::1": false,
		"::ffff:127.0.0.1": false, "::ffff:8.8.8.8": true, "64:ff9b::a00:1": false,
		"2001:db8::1": false, "2002:a00:1::": false, "2001::1": false, "ff02::1": false, "::127.0.0.1": false,
	}
	for s, want := range cases {
		if got := PublicAddr(netip.MustParseAddr(s)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestControl(t *testing.T) {
	c := Control(PublicAddr)
	if err := c("tcp", "127.0.0.1:443", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback allowed: %v", err)
	}
	if err := c("tcp", "garbage", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("unparsable allowed: %v", err)
	}
	if err := c("tcp", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("public refused: %v", err)
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ips, ok := f[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no such host")
}

func TestDialerRefusesPrivateResolution(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	res := fakeResolver{
		"rebind.example": {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
		"local.example":  {netip.MustParseAddr("127.0.0.1")},
	}
	d := &Dialer{Resolver: res, Port: port}
	ctx := context.Background()
	for _, host := range []string{"rebind.example:443", "local.example:443", "127.0.0.1:443", "[::1]:443"} {
		if _, err := d.DialContext(ctx, "tcp", host); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: want blocked, got %v", host, err)
		}
	}
	if _, err := d.DialContext(ctx, "tcp", "missing.example:443"); err == nil {
		t.Error("unresolvable host dialled")
	}
	// A permissive policy connects to the checked address.
	open := &Dialer{Resolver: res, Port: port, OK: func(netip.Addr) bool { return true }}
	conn, err := open.DialContext(ctx, "tcp", "local.example:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestInPrefixes(t *testing.T) {
	list := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	if !InPrefixes(netip.MustParseAddr("::ffff:10.1.2.3"), list) || InPrefixes(netip.MustParseAddr("11.0.0.1"), list) {
		t.Fatal("InPrefixes")
	}
}
