package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"math/big"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
)

const (
	// SiteIDLength is the length of a site ID and of its default host label.
	SiteIDLength = 10
	// TokenRandomBytes is the entropy of an API token.
	TokenRandomBytes = 32
	// TokenDisplayPrefixLength is how many characters of a token are stored for display.
	TokenDisplayPrefixLength = len(brand.TokenPrefix) + 6

	lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"
	base62     = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

func randomString(alphabet string, n int) string {
	max := big.NewInt(int64(len(alphabet)))
	var b strings.Builder
	b.Grow(n)
	for range n {
		i, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

// NewSiteID returns 10 random characters from a-z0-9.
func NewSiteID() string { return randomString(lowerAlnum, SiteIDLength) }

// NewDeployID returns a time-sortable release ID such as 20261112-101530-x7k2m9.
func NewDeployID(now time.Time) string {
	return now.UTC().Format("20060102-150405") + "-" + randomString(lowerAlnum, 6)
}

// NewToken returns a new API token value: brand prefix plus 43 base62 characters
// (more than 32 random bytes of entropy).
func NewToken() string {
	return brand.TokenPrefix + randomString(base62, 43)
}

// TokenDisplayPrefix returns the part of a token stored for display.
func TokenDisplayPrefix(token string) string {
	if len(token) <= TokenDisplayPrefixLength {
		return token
	}
	return token[:TokenDisplayPrefixLength]
}

// NewSecret returns 32 random bytes encoded as unpadded base64url, used for
// session cookies and invite links.
func NewSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashSecret returns SHA-256 of a token, session or invite secret.
func HashSecret(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// EqualHash compares two hashes in constant time.
func EqualHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
