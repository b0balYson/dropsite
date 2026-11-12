package domain

import (
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// slugPattern is the slug format from the PRD: 3–63 characters.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])$`)

// hostPattern mirrors the @site matcher in deploy/Caddyfile. A hostname that
// does not match it never reaches the by-host directory in Caddy, so control
// must never write such a name either.
var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// BuiltinReservedSlugs can never be used as slugs.
var BuiltinReservedSlugs = []string{
	"www", "api", "app", "admin", "mail", "mcp", "static", "cdn", "ns1", "ns2",
}

const (
	// MaxTitleLength is the maximum site title length in characters.
	MaxTitleLength = 120
	// MaxTokenNameLength is the maximum API token name length in characters.
	MaxTokenNameLength = 60
	// MinPasswordLength is the minimum password length in characters.
	MinPasswordLength = 10
	// MaxPasswordLength bounds hashing cost.
	MaxPasswordLength = 256
	maxHostnameLength = 253
)

// IsValidHostname reports whether h is a lowercase ASCII DNS name accepted by Caddy.
func IsValidHostname(h string) bool {
	return len(h) <= maxHostnameLength && hostPattern.MatchString(h)
}

// NormalizeHostname lowercases h and strips a port and a trailing dot.
// It does not convert IDNs; callers convert to punycode first.
func NormalizeHostname(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

// ReservedSlugs is the set of slugs nobody may use.
type ReservedSlugs struct {
	names []string
}

// NewReservedSlugs builds the reserved set from the builtin list, the API host
// label (when the API host sits directly under the base domain) and extras.
func NewReservedSlugs(baseDomain, apiHost string, extra ...[]string) ReservedSlugs {
	names := slices.Clone(BuiltinReservedSlugs)
	if label, ok := strings.CutSuffix(apiHost, "."+baseDomain); ok && !strings.Contains(label, ".") {
		names = append(names, label)
	}
	for _, list := range extra {
		for _, n := range list {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
				names = append(names, n)
			}
		}
	}
	slices.Sort(names)
	return ReservedSlugs{names: slices.Compact(names)}
}

// Contains reports whether slug is reserved.
func (r ReservedSlugs) Contains(slug string) bool {
	_, found := slices.BinarySearch(r.names, slug)
	return found
}

// List returns the reserved names in sorted order.
func (r ReservedSlugs) List() []string { return slices.Clone(r.names) }

// ValidateSlug checks format and reservation. Uniqueness is checked by storage.
func ValidateSlug(slug string, reserved ReservedSlugs) error {
	if !slugPattern.MatchString(slug) {
		return E(KindInvalid, CodeSlugInvalid,
			"slug must be 3-63 characters of a-z, 0-9 and hyphens, starting and ending with a letter or digit")
	}
	if reserved.Contains(slug) {
		return E(KindInvalid, CodeSlugReserved, "slug is reserved").
			WithParams(map[string]any{"slug": slug})
	}
	return nil
}

// ValidateTitle trims and checks a site title.
func ValidateTitle(title string) (string, error) {
	t := strings.TrimSpace(title)
	if t == "" || utf8.RuneCountInString(t) > MaxTitleLength || strings.ContainsAny(t, "\x00\r\n") {
		return "", E(KindInvalid, CodeTitleInvalid, "title must be 1-120 characters on one line").
			WithParams(map[string]any{"max": MaxTitleLength})
	}
	return t, nil
}

// ValidateTokenName trims and checks an API token name.
func ValidateTokenName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > MaxTokenNameLength || strings.ContainsAny(n, "\x00\r\n") {
		return "", E(KindInvalid, CodeTokenInvalid, "token name must be 1-60 characters on one line").
			WithParams(map[string]any{"max": MaxTokenNameLength})
	}
	return n, nil
}

// NormalizeEmail validates an email address and returns it trimmed.
// Uniqueness is case-insensitive; the stored form keeps the original case.
func NormalizeEmail(email string) (string, error) {
	e := strings.TrimSpace(email)
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || len(e) > 254 || !strings.Contains(e[strings.LastIndexByte(e, '@')+1:], ".") {
		return "", E(KindInvalid, CodeEmailInvalid, "invalid email address")
	}
	return e, nil
}

// ValidatePassword checks password length.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinPasswordLength {
		return E(KindInvalid, CodePasswordShort, "password must be at least 10 characters").
			WithParams(map[string]any{"min": MinPasswordLength})
	}
	if len(pw) > MaxPasswordLength {
		return E(KindInvalid, CodeInvalidInput, "password is too long")
	}
	return nil
}
