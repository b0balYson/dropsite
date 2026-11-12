package domain

import (
	"strings"
	"time"
)

// Connect codes are short one-time secrets issued in the UI and exchanged for
// an API token (CLI) or an OAuth grant (chat clients).
const (
	// ConnectCodeLength is the number of symbols; 13 × 5 bits = 65 bits.
	ConnectCodeLength = 13
	// connectAlphabet is Crockford base32 without I, L, O, U: no symbol can
	// be confused with another when read aloud or retyped.
	connectAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// NewConnectCode returns a random code formatted as XXXX-XXXX-XXXXX.
func NewConnectCode() string {
	raw := randomString(connectAlphabet, ConnectCodeLength)
	return raw[:4] + "-" + raw[4:8] + "-" + raw[8:]
}

// NormalizeConnectCode uppercases the input and drops spaces and dashes. It
// returns "" when the result cannot be a connect code.
func NormalizeConnectCode(s string) string {
	if len(s) > 64 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			continue
		case strings.ContainsRune(connectAlphabet, r):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != ConnectCodeLength {
		return ""
	}
	return b.String()
}

// ConnectCode is a stored connect code; Hash is SHA-256 of the normalized code.
type ConnectCode struct {
	ID        int64
	UserID    int64
	Hash      []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// OAuthClient is a dynamically registered public client.
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
	CreatedIP    string
}

// OAuthCode is an authorization code waiting to be exchanged.
type OAuthCode struct {
	ID          int64
	Hash        []byte
	UserID      int64
	ClientID    string
	ClientName  string
	RedirectURI string
	Challenge   string
	Resource    string
	Scope       string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	UsedAt      *time.Time
	GrantID     *int64
}

// OAuthGrant is one connected application of a user: the root that every
// token issued for it hangs off, and the unit of revocation.
type OAuthGrant struct {
	ID           int64
	UserID       int64
	ClientID     string
	ClientName   string
	RedirectHost string
	Scope        string
	Resource     string
	CreatedAt    time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
}

// OAuth token kinds.
const (
	OAuthAccess  = "access"
	OAuthRefresh = "refresh"
)

// OAuthToken is an access or refresh token; Hash is SHA-256 of its value.
type OAuthToken struct {
	ID        int64
	Hash      []byte
	Kind      string
	GrantID   int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// Token prefixes of OAuth tokens; API tokens use brand.TokenPrefix.
const (
	OAuthAccessPrefix  = "dsa_"
	OAuthRefreshPrefix = "dsr_"
)

// NewOAuthToken returns a token value with the given prefix and 43 base62
// characters of randomness.
func NewOAuthToken(prefix string) string { return prefix + randomString(base62, 43) }

// NewOAuthClientID returns a random identifier for a registered client.
func NewOAuthClientID() string { return "dsc_" + randomString(base62, 24) }
