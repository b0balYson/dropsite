package domain

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// HostStatus is the externally visible state of a hostname.
type HostStatus string

const (
	HostActive   HostStatus = "active"   // serves the site
	HostPending  HostStatus = "pending"  // external, waiting for an administrator
	HostRejected HostStatus = "rejected" // external, rejected or un-approved by an administrator
)

// MaxRejectReasonLength bounds the reason an administrator gives for a rejection.
const MaxRejectReasonLength = 200

// DefaultMaxDomainsPerSite is the default number of external domains per site.
const DefaultMaxDomainsPerSite = 5

// Status derives the hostname status. Only external hostnames can be inactive.
func (h Hostname) Status() HostStatus {
	switch {
	case h.Verified:
		return HostActive
	case h.ReviewedAt == nil:
		return HostPending
	default:
		return HostRejected
	}
}

// Active reports whether the hostname serves the site (and may get a certificate).
func (h Hostname) Active() bool { return h.Verified }

// ExternalHostRules are the zones an external domain must stay out of.
type ExternalHostRules struct {
	BaseDomain string // the name and everything below it (slugs live there)
	APIHost    string // the name and everything below it
	// UIHost is the host name of the web interface. The name and its parent
	// domains are refused: a site on a parent domain could set a cookie for
	// it that the browser would send to the interface. An IP forbids nothing.
	UIHost string
}

var (
	errDomainInvalid = E(KindInvalid, CodeDomainInvalid,
		"domain must be a name such as promo.example.com: at least two labels of a-z, 0-9 and hyphens, without scheme, path or port")
	errDomainForbidden = E(KindInvalid, CodeDomainForbidden,
		"this domain belongs to the installation itself; use a slug for addresses under the base domain")
)

// NormalizeExternalHost validates a domain a user wants to bind to a site and
// returns its stored form: lower case, punycode, no trailing dot.
func NormalizeExternalHost(raw string, rules ExternalHostRules) (string, error) {
	h := strings.TrimSpace(raw)
	if h == "" || strings.ContainsAny(h, "/:@[]\\ \t") || !utf8.ValidString(h) {
		return "", errDomainInvalid
	}
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", errDomainInvalid
	}
	ascii = strings.ToLower(ascii)
	last := ascii[strings.LastIndexByte(ascii, '.')+1:]
	if !IsValidHostname(ascii) || !strings.Contains(ascii, ".") || strings.Trim(last, "0123456789") == "" {
		return "", errDomainInvalid
	}
	if inZone(ascii, rules.BaseDomain) || inZone(ascii, rules.APIHost) || inZone(rules.UIHost, ascii) {
		return "", errDomainForbidden.WithParams(map[string]any{"domain": ascii})
	}
	return ascii, nil
}

// inZone reports whether host equals zone or lies below it. An empty or IP
// zone or host never matches.
func inZone(host, zone string) bool {
	host, zone = strings.ToLower(host), strings.ToLower(zone)
	if host == "" || zone == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	return host == zone || strings.HasSuffix(host, "."+zone)
}

// ValidateRejectReason trims and checks a rejection reason.
func ValidateRejectReason(reason string) (string, error) {
	r := strings.TrimSpace(reason)
	if r == "" || utf8.RuneCountInString(r) > MaxRejectReasonLength || strings.ContainsAny(r, "\x00\r\n") {
		return "", E(KindInvalid, CodeDomainReasonInvalid, "reason must be 1-200 characters on one line").
			WithParams(map[string]any{"max": MaxRejectReasonLength})
	}
	return r, nil
}
