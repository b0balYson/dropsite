package forms

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"
)

func TestEscalationLevelsCeilingAndDecay(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	e := NewEscalator(func() time.Time { return now })
	var got []int
	for range 7 {
		got = append(got, e.Next("1.2.3.4|site"))
		now = now.Add(time.Second)
	}
	want := []int{0, 1, 2, 3, 4, 4, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("levels %v, want %v", got, want)
		}
	}
	if e.Next("5.6.7.8|site") != 0 {
		t.Fatal("other IP must start at the base level")
	}
	now = now.Add(EscalationWindow)
	if l := e.Next("1.2.3.4|site"); l != 0 {
		t.Fatalf("level after the window: %d", l)
	}
	e.Penalize("9.9.9.9|site")
	e.Penalize("9.9.9.9|site")
	if l := e.Next("9.9.9.9|site"); l != 2 {
		t.Fatalf("penalized level %d", l)
	}
	if e.Level("9.9.9.9|site") != 3 {
		t.Fatal("level after challenge")
	}
}

func TestDifficulty(t *testing.T) {
	for level, want := range map[int]int{0: 5000, 1: 20000, 2: 80000, 3: 320000, 4: 1280000, 9: 1280000} {
		if got := Difficulty(5000, level); got != want {
			t.Errorf("level %d: %d", level, got)
		}
	}
}

func TestAltchaDifficultyCannotBeForged(t *testing.T) {
	a := testAltcha()
	ch, err := a.Challenge("promo.test", PurposeForm, Difficulty(MinAltchaMaxNumber, 1), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload := solveAltcha(t, ch)
	raw, _ := base64.StdEncoding.DecodeString(payload)
	for _, tamper := range []func(p *altcha.Payload){
		func(p *altcha.Payload) { p.Challenge.Parameters.Cost = 1 },
		func(p *altcha.Payload) { p.Challenge.Parameters.KeyPrefix = "00" },
		func(p *altcha.Payload) { p.Challenge.Parameters.ExpiresAt += 3600 },
		func(p *altcha.Payload) { p.Challenge.Parameters.Data["purpose"] = PurposeLogin },
	} {
		var p altcha.Payload
		_ = json.Unmarshal(raw, &p)
		tamper(&p)
		b, _ := json.Marshal(p)
		if _, err := a.Verify(base64.StdEncoding.EncodeToString(b), "promo.test", PurposeForm); !errors.Is(err, ErrAltcha) {
			t.Fatal("tampered parameters accepted")
		}
	}
}
