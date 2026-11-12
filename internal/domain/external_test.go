package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeExternalHost(t *testing.T) {
	zones := ExternalHostRules{BaseDomain: "example.com", APIHost: "api.example.com", UIHost: "panel.corp.example"}
	cases := []struct {
		in, want, code string
	}{
		{"promo.example.ru", "promo.example.ru", ""},
		{"  Promo.Example.RU.  ", "promo.example.ru", ""},
		{"example.ru", "example.ru", ""},
		{"promo.external.localhost", "promo.external.localhost", ""},
		{"Промо.Пример.РФ.", "xn--l1adbbf.xn--e1afmkfd.xn--p1ai", ""},
		{"xn--l1adbbf.xn--e1afmkfd.xn--p1ai", "xn--l1adbbf.xn--e1afmkfd.xn--p1ai", ""},
		{"a.b.c.d.example.org", "a.b.c.d.example.org", ""},
		{"", "", CodeDomainInvalid},
		{"localhost", "", CodeDomainInvalid},
		{"203.0.113.7", "", CodeDomainInvalid},
		{"[2001:db8::1]", "", CodeDomainInvalid},
		{"promo.example.ru:8443", "", CodeDomainInvalid},
		{"https://promo.example.ru", "", CodeDomainInvalid},
		{"promo.example.ru/path", "", CodeDomainInvalid},
		{"promo example.ru", "", CodeDomainInvalid},
		{"promo_x.example.ru", "", CodeDomainInvalid},
		{"-promo.example.ru", "", CodeDomainInvalid},
		{"promo..example.ru", "", CodeDomainInvalid},
		{"*.example.ru", "", CodeDomainInvalid},
		{strings.Repeat("a", 64) + ".ru", "", CodeDomainInvalid},
		{strings.Repeat("abcdefghi.", 26) + "ru", "", CodeDomainInvalid},
		{"example.com", "", CodeDomainForbidden},
		{"Promo.Example.COM", "", CodeDomainForbidden},
		{"deep.promo.example.com", "", CodeDomainForbidden},
		{"api.example.com", "", CodeDomainForbidden},
		{"panel.corp.example", "", CodeDomainForbidden},
		{"corp.example", "", CodeDomainForbidden},
		{"other.corp.example", "other.corp.example", ""},
		{"notexample.com", "notexample.com", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NormalizeExternalHost(tc.in, zones)
			if tc.code != "" {
				if de := AsError(err); de == nil || de.Code != tc.code {
					t.Fatalf("want %s, got %q, %v", tc.code, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestExternalRulesAPIOutsideBase(t *testing.T) {
	zones := ExternalHostRules{BaseDomain: "sites.example.com", APIHost: "api.corp.example", UIHost: "10.0.0.5"}
	for _, h := range []string{"api.corp.example", "x.api.corp.example"} {
		if _, err := NormalizeExternalHost(h, zones); AsError(err).Code != CodeDomainForbidden {
			t.Fatalf("%s: want forbidden, got %v", h, err)
		}
	}
	// An IP UI host forbids nothing; the parent of the API host is fine.
	if got, err := NormalizeExternalHost("corp.example", zones); err != nil || got != "corp.example" {
		t.Fatalf("corp.example: %q, %v", got, err)
	}
}

func TestHostnameStatus(t *testing.T) {
	now := time.Now()
	cases := []struct {
		h    Hostname
		want HostStatus
	}{
		{Hostname{Kind: HostDefault, Verified: true}, HostActive},
		{Hostname{Kind: HostExternal, Verified: true, ReviewedAt: &now}, HostActive},
		{Hostname{Kind: HostExternal}, HostPending},
		{Hostname{Kind: HostExternal, ReviewedAt: &now, RejectReason: "no"}, HostRejected},
	}
	for _, tc := range cases {
		if got := tc.h.Status(); got != tc.want {
			t.Fatalf("%+v: got %s want %s", tc.h, got, tc.want)
		}
		if tc.h.Active() != (tc.want == HostActive) {
			t.Fatalf("%+v: Active mismatch", tc.h)
		}
	}
}

func TestValidateRejectReason(t *testing.T) {
	if r, err := ValidateRejectReason("  not ours  "); err != nil || r != "not ours" {
		t.Fatalf("got %q, %v", r, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("я", MaxRejectReasonLength+1), "a\nb"} {
		if _, err := ValidateRejectReason(bad); AsError(err).Code != CodeDomainReasonInvalid {
			t.Fatalf("%q: want reason error, got %v", bad, err)
		}
	}
	if _, err := ValidateRejectReason(strings.Repeat("я", MaxRejectReasonLength)); err != nil {
		t.Fatalf("max length rejected: %v", err)
	}
}
