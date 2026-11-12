package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// loginSolution solves a base-difficulty login challenge for host without
// touching the escalation state, for tests about the password itself.
func (e *env) loginSolution(host string) string {
	e.t.Helper()
	ch, err := e.svc.altcha.Challenge(host, forms.PurposeLogin, forms.MinAltchaMaxNumber, time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return solve(e.t, ch)
}

// visitorLogin logs in with a fresh valid Altcha solution.
func (e *env) visitorLogin(host, password, ip string) (VisitorCookie, error) {
	e.t.Helper()
	return e.svc.VisitorLogin(context.Background(), VisitorLoginInput{
		Host: host, Password: password, Altcha: e.loginSolution(host), IP: ip,
	})
}

// minAltcha sets the base difficulty to the minimum so escalated levels stay
// cheap to solve in tests.
func (e *env) minAltcha() {
	e.t.Helper()
	m := forms.MinAltchaMaxNumber
	if _, err := e.svc.UpdateFormSettings(context.Background(), e.admin, FormSettingsUpdate{AltchaMaxNumber: &m}); err != nil {
		e.t.Fatal(err)
	}
}

func TestVisitorLoginRequiresAltcha(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v := e.protectedSite(alice, "Docs", "docs", "visitor-pass-1")
	host := "docs." + testBase
	e.minAltcha()

	login := func(payload string) error {
		_, err := e.svc.VisitorLogin(ctx, VisitorLoginInput{Host: host, Password: "visitor-pass-1", Altcha: payload, IP: "198.51.100.1"})
		return err
	}
	formCh, err := e.svc.AltchaChallenge(ctx, host, "198.51.100.1")
	if err != nil {
		t.Fatal(err)
	}
	otherCh, err := e.svc.altcha.Challenge(v.Site.ID+"."+testBase, forms.PurposeLogin, forms.MinAltchaMaxNumber, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := e.svc.hasher.verifies.Load()
	for name, payload := range map[string]string{
		"missing":       "",
		"garbage":       "not-base64!",
		"form solution": solve(t, formCh),
		"other address": solve(t, otherCh),
	} {
		// The right password does not help without a valid solution.
		if err := login(payload); !errors.Is(err, ErrVisitorAltcha) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := e.svc.hasher.verifies.Load() - before; n != 0 {
		t.Fatalf("argon2 ran %d times without a valid solution", n)
	}

	ch, err := e.svc.VisitorAltchaChallenge(ctx, host, "198.51.100.1")
	if err != nil {
		t.Fatal(err)
	}
	good := solve(t, ch)
	if err := login(good); err != nil {
		t.Fatalf("valid solution: %v", err)
	}
	if n := e.svc.hasher.verifies.Load() - before; n != 1 {
		t.Fatalf("argon2 ran %d times", n)
	}
	if err := login(good); !errors.Is(err, ErrVisitorAltcha) {
		t.Fatalf("replayed solution: %v", err)
	}
	// A login solution is not accepted by the site's forms either.
	if _, err := e.svc.SetFormSiteSettings(ctx, alice, v.Site.ID, true); err != nil {
		t.Fatal(err)
	}
	ch, _ = e.svc.VisitorAltchaChallenge(ctx, host, "198.51.100.2")
	p := post(host, "a", "1")
	p.Data.Altcha = solve(t, ch)
	_, err = e.svc.SubmitForm(ctx, p)
	wantCode(t, err, domain.CodeFormCaptcha)
}

func TestVisitorAltchaChallengeOnlyForProtectedSites(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	open, _ := e.svc.CreateSite(ctx, alice, "Open", "")
	e.protectedSite(alice, "Docs", "docs", "visitor-pass-1")
	for _, h := range []string{open.Site.ID + "." + testBase, "unknown." + testBase, "..", ""} {
		_, err := e.svc.VisitorAltchaChallenge(ctx, h, "1.1.1.1")
		wantNotFound(t, err)
	}
	ch, err := e.svc.VisitorAltchaChallenge(ctx, "docs."+testBase, "1.1.1.1")
	if err != nil || ch.Parameters.Data["purpose"] != forms.PurposeLogin || ch.Parameters.Data["host"] != "docs."+testBase {
		t.Fatalf("challenge %+v %v", ch.Parameters.Data, err)
	}
	// Issuance is rate limited per IP.
	for range FormChallengesPerIP {
		_, _ = e.svc.VisitorAltchaChallenge(ctx, "docs."+testBase, "2.2.2.2")
	}
	_, err = e.svc.VisitorAltchaChallenge(ctx, "docs."+testBase, "2.2.2.2")
	wantCode(t, err, domain.CodeRateLimited)
}

// counterOf solves ch and returns the secret counter, which lies in
// [max/2, max) of the challenge's difficulty.
func counterOf(t *testing.T, ch altcha.Challenge) int {
	t.Helper()
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeyPBKDF2(), Timeout: time.Minute})
	if err != nil || sol == nil {
		t.Fatalf("solve %v", err)
	}
	return sol.Counter
}

func TestVisitorAltchaEscalation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	e.protectedSite(alice, "Docs", "docs", "visitor-pass-1")
	host := "docs." + testBase
	e.minAltcha()
	base := forms.MinAltchaMaxNumber
	level := func(c int) int {
		for l := range forms.MaxEscalationLevel + 1 {
			if c < forms.Difficulty(base, l) {
				return l
			}
		}
		return -1
	}
	attempt := func(ip, password string) int {
		ch, err := e.svc.VisitorAltchaChallenge(ctx, host, ip)
		if err != nil {
			t.Fatal(err)
		}
		lvl := level(counterOf(t, ch))
		_, _ = e.svc.VisitorLogin(ctx, VisitorLoginInput{Host: host, Password: password, Altcha: solve(t, ch), IP: ip})
		return lvl
	}
	// Each failed attempt costs a challenge and a penalty: base, ×16, ×256.
	for i, want := range []int{0, 2, 4} {
		if got := attempt("192.0.2.1", "wrong-password"); got != want {
			t.Fatalf("attempt %d: level %d, want %d", i+1, got, want)
		}
	}
	// The ceiling holds; another IP and the site's forms are not affected.
	if got := attempt("192.0.2.1", "wrong-password"); got != forms.MaxEscalationLevel {
		t.Fatalf("ceiling: %d", got)
	}
	if got := attempt("192.0.2.2", "visitor-pass-1"); got != 0 {
		t.Fatalf("other IP: %d", got)
	}
	formCh, _ := e.svc.AltchaChallenge(ctx, host, "192.0.2.1")
	if c := counterOf(t, formCh); c >= base {
		t.Fatalf("form challenge escalated by logins: %d", c)
	}
	// An invalid solution raises the level too.
	_, _ = e.svc.VisitorLogin(ctx, VisitorLoginInput{Host: host, Password: "x", Altcha: "", IP: "192.0.2.3"})
	if got := attempt("192.0.2.3", "visitor-pass-1"); got != 1 {
		t.Fatalf("after invalid solution: %d", got)
	}
	// Levels decay with the window.
	e.clock.Advance(forms.EscalationWindow + time.Second)
	if got := attempt("192.0.2.1", "visitor-pass-1"); got != 0 {
		t.Fatalf("no decay: %d", got)
	}
}

// Requests without a valid solution count toward neither limit: they cannot
// lock a source out nor raise the difficulty for the whole site.
func TestVisitorLimitsCountOnlyPaidAttempts(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	e.protectedSite(alice, "Docs", "docs", "visitor-pass-1")
	host := "docs." + testBase
	e.minAltcha()
	for i := range VisitorFailuresPerSite + 10 {
		ip := fmt.Sprintf("203.0.113.%d", i%10)
		_, err := e.svc.VisitorLogin(ctx, VisitorLoginInput{Host: host, Password: "wrong-password", Altcha: "forged", IP: ip})
		if !errors.Is(err, ErrVisitorAltcha) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := e.visitorLogin(host, "visitor-pass-1", "203.0.113.1"); err != nil {
		t.Fatalf("source locked by unpaid attempts: %v", err)
	}
	ch, _ := e.svc.VisitorAltchaChallenge(ctx, host, "198.51.100.9")
	if c := counterOf(t, ch); c >= forms.MinAltchaMaxNumber {
		t.Fatalf("site difficulty raised by unpaid attempts: %d", c)
	}
}
