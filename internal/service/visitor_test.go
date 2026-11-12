package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

func (e *env) protectedSite(owner Actor, title, slug, password string) SiteView {
	e.t.Helper()
	ctx := context.Background()
	v, err := e.svc.CreateSite(ctx, owner, title, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	v, err = e.svc.UpdateSite(ctx, owner, v.Site.ID, SiteUpdate{Password: &password})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) decide(host, cookie string) AccessDecision {
	e.t.Helper()
	d, err := e.svc.CheckVisitorAccess(context.Background(), host, cookie)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func TestVisitorAccessFlow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	open, _ := e.svc.CreateSite(ctx, alice, "Open", "")
	v := e.protectedSite(alice, "Secret docs", "docs", "visitor-pass-1")
	host := "docs." + testBase

	if d := e.decide(open.Site.ID+"."+testBase, ""); d != AccessAllow {
		t.Fatalf("open site: %v", d)
	}
	for _, h := range []string{"unknown." + testBase, "..", "", "Docs." + testBase} {
		if d := e.decide(h, ""); d != AccessUnknown {
			t.Fatalf("%q: %v", h, d)
		}
	}
	if d := e.decide(host, ""); d != AccessLogin {
		t.Fatalf("no cookie: %v", d)
	}
	title, err := e.svc.VisitorSite(ctx, host)
	if err != nil || title != "Secret docs" {
		t.Fatalf("title %q %v", title, err)
	}
	_, err = e.svc.VisitorSite(ctx, open.Site.ID+"."+testBase)
	wantNotFound(t, err)

	_, err = e.visitorLogin(host, "wrong-password", "1.2.3.4")
	wantCode(t, err, domain.CodeBadCredentials)
	c, err := e.visitorLogin(host, "visitor-pass-1", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Expires.Equal(e.clock.Now().Add(VisitorCookieTTL)) {
		t.Fatalf("expires %v", c.Expires)
	}
	if d := e.decide(host, c.Value); d != AccessAllow {
		t.Fatalf("valid cookie: %v", d)
	}
	// The cookie is for the site, so it also works on its default address.
	if d := e.decide(v.Site.ID+"."+testBase, c.Value); d != AccessAllow {
		t.Fatalf("default host: %v", d)
	}

	// Another protected site does not accept it.
	other := e.protectedSite(alice, "Other", "", "visitor-pass-2")
	if d := e.decide(other.Site.ID+"."+testBase, c.Value); d != AccessLogin {
		t.Fatalf("foreign cookie: %v", d)
	}

	// Tampering with any field breaks the signature.
	parts := strings.Split(c.Value, ".")
	for i, bad := range []string{
		strings.Join([]string{parts[0], "9999999999", parts[2], parts[3]}, "."),
		strings.Join([]string{parts[0], parts[1], "7", parts[3]}, "."),
		strings.Join([]string{parts[0], parts[1], parts[2], parts[3][:len(parts[3])-2] + "AA"}, "."),
		strings.Join([]string{other.Site.ID, parts[1], parts[2], parts[3]}, "."),
		c.Value + ".x", "garbage", parts[3],
	} {
		if d := e.decide(host, bad); d != AccessLogin {
			t.Fatalf("tampered %d accepted", i)
		}
	}

	// Expiry after 7 days.
	e.clock.Advance(VisitorCookieTTL)
	if d := e.decide(host, c.Value); d != AccessLogin {
		t.Fatalf("expired cookie: %v", d)
	}
}

func TestPasswordChangeRevokesCookies(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v := e.protectedSite(alice, "Docs", "", "visitor-pass-1")
	host := v.Site.ID + "." + testBase
	c, err := e.visitorLogin(host, "visitor-pass-1", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Password: sptr("visitor-pass-2")}); err != nil {
		t.Fatal(err)
	}
	if d := e.decide(host, c.Value); d != AccessLogin {
		t.Fatalf("old cookie after change: %v", d)
	}
	// Unprotect and protect again with the same password: still revoked.
	c2, err := e.visitorLogin(host, "visitor-pass-2", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Protected: bptr(false)}); err != nil {
		t.Fatal(err)
	}
	if d := e.decide(host, c2.Value); d != AccessAllow {
		t.Fatalf("open site: %v", d)
	}
	_, err = e.visitorLogin(host, "visitor-pass-2", "1.2.3.4")
	wantNotFound(t, err)
	if _, err := e.svc.UpdateSite(ctx, alice, v.Site.ID, SiteUpdate{Password: sptr("visitor-pass-2")}); err != nil {
		t.Fatal(err)
	}
	if d := e.decide(host, c2.Value); d != AccessLogin {
		t.Fatalf("cookie survived re-protection: %v", d)
	}
}

func TestVisitorBruteForce(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v := e.protectedSite(alice, "Docs", "", "visitor-pass-1")
	host := v.Site.ID + "." + testBase

	for range VisitorFailuresPerIP {
		_, err := e.visitorLogin(host, "nope-nope-nope", "1.1.1.1")
		wantCode(t, err, domain.CodeBadCredentials)
	}
	// Even the right password is refused now, from that IP.
	_, err := e.visitorLogin(host, "visitor-pass-1", "1.1.1.1")
	wantCode(t, err, domain.CodeRateLimited)
	if de := domain.AsError(err); de.RetryAfter <= 0 {
		t.Fatal("no retry hint")
	}
	// Another IP is not affected; a success does not count as a failure.
	for range 3 {
		if _, err := e.visitorLogin(host, "visitor-pass-1", "2.2.2.2"); err != nil {
			t.Fatal(err)
		}
	}
	// The window passes.
	e.clock.Advance(VisitorFailuresWindow + time.Second)
	if _, err := e.visitorLogin(host, "visitor-pass-1", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
}

// IPv6 addresses of one /64 share the per-source limit.
func TestVisitorBruteForceIPv6Prefix(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v := e.protectedSite(alice, "Docs", "", "visitor-pass-1")
	host := v.Site.ID + "." + testBase
	for i := range VisitorFailuresPerIP {
		_, err := e.visitorLogin(host, "nope-nope-nope", fmt.Sprintf("2001:db8:0:1::%x", i+1))
		wantCode(t, err, domain.CodeBadCredentials)
	}
	_, err := e.visitorLogin(host, "visitor-pass-1", "2001:db8:0:1:ffff::1")
	wantCode(t, err, domain.CodeRateLimited)
	if _, err := e.visitorLogin(host, "visitor-pass-1", "2001:db8:0:2::1"); err != nil {
		t.Fatalf("other /64: %v", err)
	}
	for in, want := range map[string]string{
		"2001:db8:0:1::5": "2001:db8:0:1::/64", "::ffff:192.0.2.1": "192.0.2.1", "192.0.2.1": "192.0.2.1", "bogus": "bogus",
	} {
		if got := visitorSource(in); got != want {
			t.Errorf("visitorSource(%q) = %q, want %q", in, got, want)
		}
	}
}

// Failures spread over many sources never lock the site: past the per-site
// threshold the right password still works, but every login challenge is
// harder.
func TestVisitorSiteThresholdRaisesDifficulty(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v := e.protectedSite(alice, "Docs", "", "visitor-pass-1")
	host := v.Site.ID + "." + testBase
	e.minAltcha()
	for i := range VisitorFailuresPerSite {
		ip := fmt.Sprintf("2001:db8:%x::1", i/VisitorFailuresPerIP)
		_, err := e.visitorLogin(host, "nope-nope-nope", ip)
		wantCode(t, err, domain.CodeBadCredentials)
	}
	if _, err := e.visitorLogin(host, "visitor-pass-1", "198.51.100.9"); err != nil {
		t.Fatalf("site locked for a visitor with the right password: %v", err)
	}
	ch, err := e.svc.VisitorAltchaChallenge(ctx, host, "198.51.100.10")
	if err != nil {
		t.Fatal(err)
	}
	if c := counterOf(t, ch); c < forms.Difficulty(forms.MinAltchaMaxNumber, VisitorSiteAltchaLevel)/2 {
		t.Fatalf("challenge not raised for the site under attack: counter %d", c)
	}
	e.clock.Advance(VisitorFailuresWindow + time.Second)
	ch, _ = e.svc.VisitorAltchaChallenge(ctx, host, "198.51.100.11")
	if c := counterOf(t, ch); c >= forms.MinAltchaMaxNumber {
		t.Fatalf("site level did not decay: counter %d", c)
	}
}

func TestVisitorCSRF(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	host := "abc." + testBase
	tok, err := e.svc.VisitorCSRFToken(ctx, host, "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	if !e.svc.CheckVisitorCSRF(ctx, host, "nonce-1", tok) {
		t.Fatal("valid token refused")
	}
	for name, ok := range map[string]bool{
		"other host":  e.svc.CheckVisitorCSRF(ctx, "x."+testBase, "nonce-1", tok),
		"other nonce": e.svc.CheckVisitorCSRF(ctx, host, "nonce-2", tok),
		"no nonce":    e.svc.CheckVisitorCSRF(ctx, host, "", tok),
		"garbage":     e.svc.CheckVisitorCSRF(ctx, host, "nonce-1", "1.x"),
		"empty":       e.svc.CheckVisitorCSRF(ctx, host, "nonce-1", ""),
	} {
		if ok {
			t.Fatalf("%s accepted", name)
		}
	}
	e.clock.Advance(VisitorCSRFTTL + time.Second)
	if e.svc.CheckVisitorCSRF(ctx, host, "nonce-1", tok) {
		t.Fatal("expired token accepted")
	}
}

func TestVisitorSecretSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.user("alice@pp.test")
	v := e.protectedSite(alice, "Docs", "", "visitor-pass-1")
	host := v.Site.ID + "." + testBase
	c, err := e.visitorLogin(host, "visitor-pass-1", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	// A new service over the same database accepts the cookie.
	svc2 := New(Deps{Config: e.svc.cfg, Repo: e.repo, Files: e.svc.files, Now: e.clock.Now, UploadDir: e.svc.uploadDir})
	d, err := svc2.CheckVisitorAccess(ctx, host, c.Value)
	if err != nil || d != AccessAllow {
		t.Fatalf("after restart: %v %v", d, err)
	}
}
