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
// lasting several seconds does not look like a hang, and adds the
// Simplified Chinese strings, which the widget package does not bundle here.
const altchaStrings = `
{
  const i18n = globalThis.$altcha.i18n;
  const patch = (lang, s) => i18n.set(lang, { ...(i18n.get(lang) || {}), ...s });
  patch("ru", { verifying: "Проверяем, что вы не робот…", waitAlert: "Проверяем, что вы не робот… Это займёт несколько секунд." });
  patch("en", { verifying: "Checking that you are not a robot…", waitAlert: "Checking that you are not a robot… This takes a few seconds." });
  patch("zh", {
    ariaLinkLabel: "Altcha（官方网站）",
    enterCode: "输入验证码",
    enterCodeAria: "输入您听到的验证码。按空格键播放音频。",
    error: "验证失败，请稍后重试。",
    expired: "验证已过期，请重试。",
    footer: '由 <a href="https://altcha.org/" target="_blank" rel="noopener" aria-label="Altcha（官方网站，在新标签页中打开）">ALTCHA</a> 提供保护',
    getAudioChallenge: "获取音频验证",
    label: "我不是机器人",
    loading: "加载中…",
    reload: "重新加载",
    verify: "验证",
    verificationRequired: "需要验证！",
    verified: "已验证",
    verifying: "正在确认您不是机器人…",
    waitAlert: "正在确认您不是机器人…需要几秒钟。",
    cancel: "取消",
    enterCodeFromImage: "请输入下方图片中的验证码以继续。"
  });
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
	DefaultAltchaMaxNumber = 100000
	MinAltchaMaxNumber     = 100
	MaxAltchaMaxNumber     = 10000000
	altchaAlgorithm        = "PBKDF2/SHA-256"
	maxAltchaPayload       = 8 << 10
)

// Purposes a challenge can be issued for. The purpose is part of the signed
// data, so a solution for a site form cannot be spent on the visitor login
// page of the same host, and vice versa. PurposeAdmin protects the sign-in
// and invitation forms of the admin interface.
const (
	PurposeForm  = "form"
	PurposeLogin = "login"
	PurposeAdmin = "admin"
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

// Challenge creates a challenge bound to host and purpose whose secret
// counter lies in [maxN/2, maxN). The difficulty is part of the signed
// parameters, so a client cannot lower it.
func (a Altcha) Challenge(host, purpose string, maxN int, now time.Time) (altcha.Challenge, error) {
	if maxN < MinAltchaMaxNumber {
		maxN = MinAltchaMaxNumber
	}
	r, err := rand.Int(rand.Reader, big.NewInt(int64(maxN-maxN/2)))
	if err != nil {
		return altcha.Challenge{}, err
	}
	counter := maxN/2 + int(r.Int64())
	expires := now.Add(AltchaTTL)
	k := kdfFor(purpose)
	return altcha.CreateChallenge(altcha.CreateChallengeOptions{
		Algorithm:              k.algorithm,
		Cost:                   k.cost,
		KeyLength:              32,
		Counter:                &counter,
		DeriveKey:              k.derive,
		ExpiresAt:              &expires,
		Data:                   map[string]any{"host": host, "purpose": purpose},
		HMACSignatureSecret:    a.SignatureSecret,
		HMACKeySignatureSecret: a.KeySecret,
	})
}

// Purposes of the invisible visitor session of the secret proxy: a session
// token, and the fresh proof of work every call of a "send" endpoint needs.
// Their challenges use plain iterated SHA-256 (one hash per attempt), which
// the small session script solves with crypto.subtle in a Web Worker;
// widgets keep PBKDF2.
const (
	PurposeSession = "session"
	PurposeSend    = "send"
)

type kdf struct {
	algorithm string
	cost      int
	derive    altcha.DeriveKeyFunc
}

func kdfFor(purpose string) kdf {
	if purpose == PurposeSession || purpose == PurposeSend {
		return kdf{algorithm: "SHA-256", cost: 1, derive: altcha.DeriveKeySHA()}
	}
	return kdf{algorithm: altchaAlgorithm, cost: AltchaCost, derive: altcha.DeriveKeyPBKDF2()}
}

// Verified identifies an accepted solution for replay protection.
type Verified struct {
	Signature string
	ExpiresAt time.Time
}

// Verify checks a widget payload (base64 JSON) for host and purpose. Expiry
// is checked by the library against the real clock.
func (a Altcha) Verify(payload, host, purpose string) (Verified, error) {
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
	k := kdfFor(purpose)
	if challengePurpose(p.Challenge.Parameters.Data) != purpose || p.Challenge.Parameters.Algorithm != k.algorithm {
		return Verified{}, ErrAltcha
	}
	res, err := altcha.VerifySolution(altcha.VerifySolutionOptions{
		Challenge:              p.Challenge,
		Solution:               p.Solution,
		DeriveKey:              k.derive,
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

// challengePurpose reads the signed purpose. Challenges issued before
// purposes existed carry none and were only ever issued for forms; they
// expire within AltchaTTL of an upgrade.
func challengePurpose(data map[string]any) string {
	v, ok := data["purpose"]
	if !ok {
		return PurposeForm
	}
	s, _ := v.(string)
	return s
}
