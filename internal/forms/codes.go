package forms

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/csv"
	"encoding/hex"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const (
	digits = "0123456789"
	base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// DigitCodeLen is the length of a code typed in by the owner.
	DigitCodeLen = 6
	// LinkCodeLen is the length of a code carried by a Telegram deep link.
	LinkCodeLen = 24
)

func randomString(alphabet string, n int) string {
	limit := big.NewInt(int64(len(alphabet)))
	var b strings.Builder
	for range n {
		i, err := rand.Int(rand.Reader, limit)
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

// DigitCode returns a 6-digit confirmation code.
func DigitCode() string { return randomString(digits, DigitCodeLen) }

// LinkCode returns a code for a Telegram /start deep link.
func LinkCode() string { return randomString(base62, LinkCodeLen) }

// NewWebhookSecret returns a webhook signing secret.
func NewWebhookSecret() string { return "whsec_" + randomString(base62, 40) }

// HashCode hashes a confirmation code for storage.
func HashCode(code string) []byte {
	h := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return h[:]
}

// MatchCode compares a typed code with a stored hash in constant time.
func MatchCode(hash []byte, code string) bool {
	return len(hash) > 0 && subtle.ConstantTimeCompare(hash, HashCode(code)) == 1
}

// csvRisky are leading characters that spreadsheets interpret as formulas.
const csvRisky = "=+-@\t\r"

func csvCell(s string) string {
	if s != "" && strings.ContainsRune(csvRisky, rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteCSV writes submissions as CSV: time, form, host and one column per
// field name (in first-seen order across all rows).
func WriteCSV(w io.Writer, subs []domain.FormSubmission) error {
	var names []string
	seen := map[string]bool{}
	for _, s := range subs {
		for _, f := range s.Fields {
			if !seen[f.Name] {
				seen[f.Name] = true
				names = append(names, f.Name)
			}
		}
	}
	cw := csv.NewWriter(w)
	header := []string{"created_at", "form", "host"}
	for _, n := range names {
		header = append(header, csvCell(n))
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, s := range subs {
		vals := map[string]string{}
		for _, f := range s.Fields {
			vals[f.Name] = f.Value
		}
		row := []string{s.CreatedAt.UTC().Format(time.RFC3339), s.Form, s.Host}
		for _, n := range names {
			row = append(row, csvCell(vals[n]))
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// hexOf is a small helper for tests and signatures.
func hexOf(b []byte) string { return hex.EncodeToString(b) }
