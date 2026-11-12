package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func isLowerAlnum(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func TestValidateSlug(t *testing.T) {
	reserved := NewReservedSlugs("example.com", "api.example.com", []string{"Promo", " shop "})
	cases := []struct {
		slug string
		code string
	}{
		{"promo-may", ""},
		{"abc", ""},
		{"a1b", ""},
		{strings.Repeat("a", 63), ""},
		{"ab", CodeSlugInvalid},
		{"a", CodeSlugInvalid},
		{"-ab", CodeSlugInvalid},
		{"ab-", CodeSlugInvalid},
		{"Promo", CodeSlugInvalid},
		{"a_b", CodeSlugInvalid},
		{"a.b", CodeSlugInvalid},
		{strings.Repeat("a", 64), CodeSlugInvalid},
		{"admin", CodeSlugReserved},
		{"www", CodeSlugReserved},
		{"ns2", CodeSlugReserved},
		{"promo", CodeSlugReserved},
		{"shop", CodeSlugReserved},
		{"newsletter", ""},
		{"k3f9x-m7qa", ""},
		{"p-shop", ""},
		{"p-20261112-101530-x7k2m9", ""},
		{"p-20261112-101530-x7k2m9-k3f9x2m7qa", CodeSlugReserved},
		{"p-00000000-000000-000000-0000000000", CodeSlugReserved},
	}
	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			err := ValidateSlug(tc.slug, reserved)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if got := AsError(err); got == nil || got.Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
}

func TestPreviewLabel(t *testing.T) {
	got := PreviewLabel("k3f9x2m7qa", "20261112-101530-x7k2m9")
	if got != "p-20261112-101530-x7k2m9-k3f9x2m7qa" {
		t.Fatalf("label %q", got)
	}
	if !IsPreviewLabel(got) || !IsValidHostname(got) || len(got) > 63 {
		t.Fatalf("label %q must be a valid preview DNS label", got)
	}
	for _, s := range []string{"p-shop", "k3f9x2m7qa", got + ".example.com", "P-20261112-101530-X7K2M9-K3F9X2M7QA", "p-20261112-101530-x7k2m9-k3f9x2m7q"} {
		if IsPreviewLabel(s) {
			t.Errorf("%q must not be a preview label", s)
		}
	}
	if !IsPreviewLabel(PreviewLabel(NewSiteID(), NewDeployID(time.Now()))) {
		t.Fatal("generated IDs must form a preview label")
	}
}

func TestReservedSlugsAPILabel(t *testing.T) {
	r := NewReservedSlugs("example.com", "deploy.example.com")
	if !r.Contains("deploy") {
		t.Fatal("api host label must be reserved")
	}
	r = NewReservedSlugs("example.com", "api.other.org")
	if r.Contains("other") {
		t.Fatal("foreign api host must not reserve labels")
	}
}

func TestHostnames(t *testing.T) {
	valid := []string{"example.com", "k3f9x2m7qa.example.com", "a.b", "xn--80ak6aa92e.com", "a"}
	for _, h := range valid {
		if !IsValidHostname(h) {
			t.Errorf("%q should be valid", h)
		}
	}
	invalid := []string{"", "..", ".", "../sites", "a/b", "A.com", "a..b", "-a.com", "a-.com", "a.com.", "a b", "%2e%2e", "a_b.com", "a:80"}
	for _, h := range invalid {
		if IsValidHostname(h) {
			t.Errorf("%q should be invalid", h)
		}
	}
	norm := map[string]string{"A.Example.COM.": "a.example.com", "a.com:443": "a.com", " x.org ": "x.org"}
	for in, want := range norm {
		if got := NormalizeHostname(in); got != want {
			t.Errorf("NormalizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewSiteID()
		if len(id) != SiteIDLength || !isLowerAlnum(id) || !IsValidHostname(id) {
			t.Fatalf("bad site id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate site id %q", id)
		}
		seen[id] = true
	}
	d := NewDeployID(time.Date(2026, 11, 12, 10, 15, 30, 0, time.UTC))
	if !strings.HasPrefix(d, "20261112-101530-") || len(d) != 22 {
		t.Fatalf("bad deploy id %q", d)
	}
	tok := NewToken()
	if !strings.HasPrefix(tok, "ds_") || len(tok) != 46 {
		t.Fatalf("bad token %q", tok)
	}
	if TokenDisplayPrefix(tok) != tok[:9] {
		t.Fatal("bad display prefix")
	}
	if !EqualHash(HashSecret("x"), HashSecret("x")) || EqualHash(HashSecret("x"), HashSecret("y")) {
		t.Fatal("hash compare broken")
	}
	if len(NewSecret()) != 43 {
		t.Fatal("secret must be 32 bytes base64url")
	}
}

func TestValidateInputs(t *testing.T) {
	if got, err := ValidateTitle("  Промо  "); err != nil || got != "Промо" {
		t.Fatalf("title: %q %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("я", 121), "a\nb"} {
		if _, err := ValidateTitle(bad); err == nil {
			t.Errorf("title %q should fail", bad)
		}
	}
	if _, err := ValidateTokenName("laptop"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateTokenName(""); err == nil {
		t.Fatal("empty token name should fail")
	}
	for _, good := range []string{"a@b.co", "User.Name+tag@example.com"} {
		if _, err := NormalizeEmail(good); err != nil {
			t.Errorf("email %q: %v", good, err)
		}
	}
	for _, bad := range []string{"", "a", "a@b", "Name <a@b.co>", "a@b.co\nx@y.zz", "a b@c.co"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("email %q should fail", bad)
		}
	}
	if err := ValidatePassword("short"); AsError(err).Code != CodePasswordShort {
		t.Fatal("short password must fail")
	}
	if err := ValidatePassword("длинныйпароль"); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	err := ErrNotFound.WithParams(map[string]any{"id": "x"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("errors.Is must match on code")
	}
	internal := AsError(errors.New("disk on fire"))
	if internal.Kind != KindInternal || strings.Contains(internal.Message, "disk") {
		t.Fatal("internal errors must not leak the cause in Message")
	}
	if AsError(nil) != nil {
		t.Fatal("nil in, nil out")
	}
	tok := Token{}
	now := time.Now()
	if !tok.Active(now) {
		t.Fatal("token without expiry is active")
	}
	past := now.Add(-time.Second)
	tok.ExpiresAt = &past
	if tok.Active(now) {
		t.Fatal("expired token is inactive")
	}
}
