package forms

import (
	"crypto/rand"
	_ "embed" // the Altcha widget is served from the binary
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

// AltchaWidgetVersion is the embedded release of the altcha npm package
// (dist/main/altcha.min.js, MIT, see assets/altcha-LICENSE.txt).
const AltchaWidgetVersion = "3.5.0"

//go:embed assets/altcha.min.js
var altchaMain []byte

//go:embed assets/altcha-i18n-ru.js
var altchaRussian []byte

// altchaStrings replaces the generic "Verifying..." so that a proof of work
// lasting several seconds does not look like a hang.
const altchaStrings = `
{
  const i18n = globalThis.$altcha.i18n;
  const patch = (lang, s) => i18n.set(lang, { ...(i18n.get(lang) || {}), ...s });
  patch("ru", { verifying: "Проверяем, что вы не робот…", waitAlert: "Проверяем, что вы не робот… Это займёт несколько секунд." });
  patch("en", { verifying: "Checking that you are not a robot…", waitAlert: "Checking that you are not a robot… This takes a few seconds." });
}
`

// AltchaWidget is the script served at /_dropsite/altcha.js: the widget,
// its Russian translation (wrapped in a block because both files declare
// the same top-level helpers) and the status texts above.
var AltchaWidget = func() []byte {
	var b []byte
	b = append(b, altchaMain...)
	b = append(b, "\n{\n"...)
	b = append(b, altchaRussian...)
	b = append(b, "\n}\n"...)
	b = append(b, altchaStrings...)
	return b
}()

// AltchaLicense is the widget's licence text.
//
//go:embed assets/altcha-LICENSE.txt
var AltchaLicense []byte

// Altcha proof-of-work parameters.
const (
	AltchaTTL = 15 * time.Minute
	// AltchaCost is the PBKDF2 iteration count of one attempt.
	AltchaCost = 100
	// DefaultAltchaMaxNumber bounds the secret counter of the base level; the
	// expected work is about 0.75 × max × cost PBKDF2 iterations: a few
	// seconds on a phone (control measurement in design.md).
	DefaultAltchaMaxNumber = 5000
	MinAltchaMaxNumber     = 100
	MaxAltchaMaxNumber     = 1000000
	altchaAlgorithm        = "PBKDF2/SHA-256"
	maxAltchaPayload       = 8 << 10
)

// Altcha issues and verifies challenges. Challenges are deterministic: the
// server picks the counter and signs the derived key, so verification is a
// single HMAC (no key derivation per submission).
type Altcha struct {
	SignatureSecret string
	KeySecret       string
}

// ErrAltcha is returned for a missing, malformed, forged, expired or
// foreign solution.
var ErrAltcha = errors.New("altcha solution is invalid")

// Challenge creates a challenge bound to host whose secret counter lies in
// [maxN/2, maxN). The difficulty is part of the signed parameters, so a
// client cannot lower it.
func (a Altcha) Challenge(host string, maxN int, now time.Time) (altcha.Challenge, error) {
	if maxN < MinAltchaMaxNumber {
		maxN = MinAltchaMaxNumber
	}
	r, err := rand.Int(rand.Reader, big.NewInt(int64(maxN-maxN/2)))
	if err != nil {
		return altcha.Challenge{}, err
	}
	counter := maxN/2 + int(r.Int64())
	expires := now.Add(AltchaTTL)
	return altcha.CreateChallenge(altcha.CreateChallengeOptions{
		Algorithm:              altchaAlgorithm,
		Cost:                   AltchaCost,
		KeyLength:              32,
		Counter:                &counter,
		DeriveKey:              altcha.DeriveKeyPBKDF2(),
		ExpiresAt:              &expires,
		Data:                   map[string]any{"host": host},
		HMACSignatureSecret:    a.SignatureSecret,
		HMACKeySignatureSecret: a.KeySecret,
	})
}

// Verified identifies an accepted solution for replay protection.
type Verified struct {
	Signature string
	ExpiresAt time.Time
}

// Verify checks a widget payload (base64 JSON) for host. Expiry is checked
// by the library against the real clock.
func (a Altcha) Verify(payload, host string) (Verified, error) {
	if payload == "" || len(payload) > maxAltchaPayload {
		return Verified{}, ErrAltcha
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return Verified{}, ErrAltcha
	}
	var p altcha.Payload
	if err := json.Unmarshal(raw, &p); err != nil || p.Challenge.Signature == "" {
		return Verified{}, ErrAltcha
	}
	if h, _ := p.Challenge.Parameters.Data["host"].(string); h != host {
		return Verified{}, ErrAltcha
	}
	res, err := altcha.VerifySolution(altcha.VerifySolutionOptions{
		Challenge:              p.Challenge,
		Solution:               p.Solution,
		DeriveKey:              altcha.DeriveKeyPBKDF2(),
		HMACSignatureSecret:    a.SignatureSecret,
		HMACKeySignatureSecret: a.KeySecret,
	})
	if err != nil || !res.Verified || res.Expired || p.Challenge.Parameters.ExpiresAt == 0 {
		return Verified{}, ErrAltcha
	}
	return Verified{
		Signature: p.Challenge.Signature,
		ExpiresAt: time.Unix(p.Challenge.Parameters.ExpiresAt, 0).UTC(),
	}, nil
}
