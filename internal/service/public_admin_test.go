package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

const adminPass = "admin-password"

func (e *env) certMarker(host string) bool {
	e.t.Helper()
	fi, err := os.Lstat(filepath.Join(e.root, "tls-hosts", host))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	return err == nil && fi.Mode().IsRegular()
}

func TestPublicAdminOffByDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if e.svc.PublicAdminEnabled(ctx) {
		t.Fatal("public admin must be off on a new installation")
	}
	if got := e.svc.PublicAdminURL(); got != "https://admin."+testBase {
		t.Fatalf("url %q", got)
	}
	if e.certMarker("admin." + testBase) {
		t.Fatal("certificate permission exists while off")
	}
}

func TestPublicAdminURLWithPort(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SitesHTTPSPort = 18443 })
	if got := e.svc.PublicAdminURL(); got != "https://admin."+testBase+":18443" {
		t.Fatalf("url %q", got)
	}
}

func TestSetPublicAdminNeedsConfirmationAndPassword(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	host := "admin." + testBase
	for name, tc := range map[string]struct {
		confirmed bool
		password  string
		code      string
	}{
		"no confirmation": {false, adminPass, domain.CodeInvalidInput},
		"no password":     {true, "", domain.CodeInvalidInput},
		"wrong password":  {true, "wrong-password-1", domain.CodeBadCredentials},
	} {
		err := e.svc.SetPublicAdmin(ctx, e.admin, true, tc.confirmed, tc.password)
		if domain.AsError(err).Code != tc.code {
			t.Fatalf("%s: %v", name, err)
		}
		if e.svc.PublicAdminEnabled(ctx) || e.certMarker(host) {
			t.Fatalf("%s: turned on", name)
		}
	}
	if err := e.svc.SetPublicAdmin(ctx, e.admin, true, true, adminPass); err != nil {
		t.Fatal(err)
	}
	if !e.svc.PublicAdminEnabled(ctx) || !e.certMarker(host) {
		t.Fatal("not turned on")
	}
	// Turning off needs no confirmation and withdraws the certificate permission.
	if err := e.svc.SetPublicAdmin(ctx, e.admin, false, false, ""); err != nil {
		t.Fatal(err)
	}
	if e.svc.PublicAdminEnabled(ctx) || e.certMarker(host) {
		t.Fatal("not turned off")
	}
	if got := e.auditActions("settings"); len(got) != 2 || got[0] != "settings.public_admin" {
		t.Fatalf("audit %v", got)
	}
}

// Only an administrator signed in to the web interface may change it: never
// a user, an API token, an OAuth grant or an AI assistant.
func TestSetPublicAdminRefusesTokensAndAssistants(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	user := e.user("ann@pp.test")
	for name, a := range map[string]Actor{
		"user":      user,
		"api token": {User: e.admin.User, IP: e.admin.IP, TokenID: 7},
		"oauth":     {User: e.admin.User, IP: e.admin.IP, GrantID: 3},
		"mcp":       {User: e.admin.User, IP: e.admin.IP, ViaMCP: true},
	} {
		for _, on := range []bool{true, false} {
			if err := e.svc.SetPublicAdmin(ctx, a, on, true, adminPass); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("%s on=%v: %v", name, on, err)
			}
		}
		if _, err := e.svc.GetPublicAdmin(ctx, Actor{User: user.User, ViaMCP: true}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s: status visible: %v", name, err)
		}
	}
	if e.svc.PublicAdminEnabled(ctx) {
		t.Fatal("turned on")
	}
}

// The password confirmation counts against the login limits, so a stolen
// session cannot guess the password through it.
func TestSetPublicAdminPasswordIsRateLimited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var err error
	for range FailedLoginsPerIP + 1 {
		err = e.svc.SetPublicAdmin(ctx, e.admin, true, true, "wrong-password-1")
	}
	if domain.AsError(err).Kind != domain.KindRateLimited {
		t.Fatalf("want rate limited, got %v", err)
	}
}

// The command line (installer) and a restart both follow the database.
func TestPublicAdminCLIAndStartup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	host := "admin." + testBase
	if err := e.svc.SetPublicAdminCLI(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !e.svc.PublicAdminEnabled(ctx) || !e.certMarker(host) {
		t.Fatal("cli did not turn it on")
	}
	// A lost marker comes back at startup; a stray one goes away.
	if err := os.Remove(filepath.Join(e.root, "tls-hosts", host)); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.certMarker(host) {
		t.Fatal("startup did not restore the certificate permission")
	}
	if err := e.repo.DeleteSetting(ctx, SettingPublicAdmin); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if e.certMarker(host) {
		t.Fatal("startup kept the certificate permission of a disabled host")
	}
}

// A change by another process reaches the running server within the TTL.
func TestPublicAdminCacheExpires(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if e.svc.PublicAdminEnabled(ctx) {
		t.Fatal("on")
	}
	if err := e.repo.SetSetting(ctx, SettingPublicAdmin, "1"); err != nil {
		t.Fatal(err)
	}
	e.svc.publicAdmin.mu.Lock()
	e.svc.publicAdmin.checked = time.Now().Add(-publicAdminTTL)
	e.svc.publicAdmin.mu.Unlock()
	if !e.svc.PublicAdminEnabled(ctx) {
		t.Fatal("change not picked up after the TTL")
	}
}

func TestAdminAltcha(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	easiest := forms.MinAltchaMaxNumber
	if _, err := e.svc.UpdateFormSettings(ctx, e.admin, FormSettingsUpdate{AltchaMaxNumber: &easiest}); err != nil {
		t.Fatal(err)
	}
	host, ip := "admin."+testBase, "198.51.100.7"
	ch, err := e.svc.AdminAltchaChallenge(ctx, host, ip)
	if err != nil {
		t.Fatal(err)
	}
	sol := solve(t, ch)
	in := LoginInput{Email: "admin@pp.test", Password: adminPass, Host: host, IP: ip}

	// Missing, from another host, and a site form solution are refused
	// before the password is checked.
	for name, payload := range map[string]string{"missing": "", "garbage": "!!"} {
		in.Altcha = payload
		if _, err := e.svc.LoginWithAltcha(ctx, in); !errors.Is(err, ErrAdminAltcha) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	other := in
	other.Host, other.Altcha = "127.0.0.1:8082", sol
	if _, err := e.svc.LoginWithAltcha(ctx, other); !errors.Is(err, ErrAdminAltcha) {
		t.Fatalf("foreign host: %v", err)
	}
	v, err := e.svc.CreateSite(ctx, e.admin, "Docs", "docs")
	if err != nil {
		t.Fatal(err)
	}
	formCh, err := e.svc.AltchaChallenge(ctx, v.Site.ID+"."+testBase, ip)
	if err != nil {
		t.Fatal(err)
	}
	formIn := in
	formIn.Host, formIn.Altcha = v.Site.ID+"."+testBase, solve(t, formCh)
	if _, err := e.svc.LoginWithAltcha(ctx, formIn); !errors.Is(err, ErrAdminAltcha) {
		t.Fatalf("site form solution: %v", err)
	}

	// A fresh solution signs in; it cannot be spent twice.
	ch, err = e.svc.AdminAltchaChallenge(ctx, host, ip)
	if err != nil {
		t.Fatal(err)
	}
	in.Altcha = solve(t, ch)
	if _, err := e.svc.LoginWithAltcha(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LoginWithAltcha(ctx, in); !errors.Is(err, ErrAdminAltcha) {
		t.Fatalf("replay: %v", err)
	}
}

// An IP past the login limit is refused before any proof of work is spent.
func TestAdminAltchaAfterLoginLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ip := "198.51.100.8"
	for range FailedLoginsPerIP {
		_, _ = e.svc.Login(ctx, "x@pp.test", "wrong-password-1", ip, "")
	}
	_, err := e.svc.LoginWithAltcha(ctx, LoginInput{Email: "admin@pp.test", Password: adminPass, Host: "h", IP: ip})
	if domain.AsError(err).Kind != domain.KindRateLimited {
		t.Fatalf("want rate limited, got %v", err)
	}
}

// Opening the sign-in page again and again does not make the check harder
// (everybody behind the SSH tunnel shares one address); a failed sign-in
// raises it one step, never more.
func TestAdminAltchaEscalatesOnFailuresOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ip := "198.51.100.9"
	for range 5 {
		if _, err := e.svc.AdminAltchaChallenge(ctx, "h", ip); err != nil {
			t.Fatal(err)
		}
	}
	if lvl := e.svc.escalator.Level(adminAltchaKey(ip)); lvl != 0 {
		t.Fatalf("level after challenges %d", lvl)
	}
	for range 3 {
		_ = e.svc.CheckAdminAltcha(ctx, "h", "garbage", ip)
	}
	if lvl := e.svc.escalator.Level(adminAltchaKey(ip)); lvl == 0 {
		t.Fatal("failures did not escalate")
	}
	if got := min(e.svc.escalator.Level(adminAltchaKey(ip)), adminAltchaMaxLevel); got != 1 {
		t.Fatalf("issued level %d", got)
	}
}
