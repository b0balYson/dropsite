package forms

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

func testAltcha() Altcha {
	return Altcha{SignatureSecret: "sig-secret", KeySecret: "key-secret"}
}

// SolveAltcha solves a challenge like the widget does and returns the
// base64 payload for the "altcha" field.
func solveAltcha(t *testing.T, ch altcha.Challenge) string {
	t.Helper()
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: 30 * time.Second})
	if err != nil || sol == nil {
		t.Fatalf("solve: %v", err)
	}
	b, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	return base64.StdEncoding.EncodeToString(b)
}

func TestAltchaRoundTrip(t *testing.T) {
	a := testAltcha()
	ch, err := a.Challenge("promo.test", MinAltchaMaxNumber, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ch.Parameters.Algorithm != "PBKDF2/SHA-256" || ch.Parameters.KeySignature == "" || ch.Parameters.ExpiresAt == 0 {
		t.Fatalf("challenge %+v", ch.Parameters)
	}
	payload := solveAltcha(t, ch)
	v, err := a.Verify(payload, "promo.test")
	if err != nil || v.Signature != ch.Signature || v.ExpiresAt.Unix() != ch.Parameters.ExpiresAt {
		t.Fatalf("verify %+v %v", v, err)
	}
	if _, err := a.Verify(payload, "other.test"); err != ErrAltcha {
		t.Fatal("solution accepted for another host")
	}
	other := a
	other.SignatureSecret = "forged"
	if _, err := other.Verify(payload, "promo.test"); err != ErrAltcha {
		t.Fatal("forged signature accepted")
	}
}

func TestAltchaRejectsExpiredAndTampered(t *testing.T) {
	a := testAltcha()
	ch, err := a.Challenge("promo.test", MinAltchaMaxNumber, time.Now().Add(-AltchaTTL-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(solveAltcha(t, ch), "promo.test"); err != ErrAltcha {
		t.Fatal("expired challenge accepted")
	}
	ch, _ = a.Challenge("promo.test", MinAltchaMaxNumber, time.Now())
	payload := solveAltcha(t, ch)
	raw, _ := base64.StdEncoding.DecodeString(payload)
	var p altcha.Payload
	_ = json.Unmarshal(raw, &p)
	p.Solution.DerivedKey = "00" + p.Solution.DerivedKey[2:]
	if p.Solution.DerivedKey[2:] == "" {
		t.Fatal("empty key")
	}
	p.Solution.DerivedKey = string(bytes.Repeat([]byte("ab"), 32))
	b, _ := json.Marshal(p)
	if _, err := a.Verify(base64.StdEncoding.EncodeToString(b), "promo.test"); err != ErrAltcha {
		t.Fatal("wrong derived key accepted")
	}
	test, _ := json.Marshal(map[string]any{"challenge": nil, "solution": nil, "test": true})
	for _, bad := range []string{"", "!!!", base64.StdEncoding.EncodeToString([]byte("{}")), base64.StdEncoding.EncodeToString(test)} {
		if _, err := a.Verify(bad, "promo.test"); err != ErrAltcha {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestAltchaWidgetEmbedded(t *testing.T) {
	if len(AltchaWidget) < 50_000 || !bytes.Contains(AltchaWidget, []byte("altcha-widget")) {
		t.Fatalf("widget %d bytes", len(AltchaWidget))
	}
	if !bytes.Contains(AltchaLicense, []byte("MIT License")) {
		t.Fatal("license missing")
	}
}
