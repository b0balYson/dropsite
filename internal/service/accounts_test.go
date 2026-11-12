package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
)

func TestBootstrapIsOneShot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.cfg.AdminPassword = "another-password"
	created, err := e.svc.Bootstrap(ctx)
	if err != nil || created {
		t.Fatalf("second bootstrap must be a no-op: %v %v", created, err)
	}
	if _, err := e.svc.Login(ctx, "admin@pp.test", "another-password", "1.1.1.1", ""); err == nil {
		t.Fatal("password must not change on restart")
	}
	if _, err := e.svc.Login(ctx, "ADMIN@pp.test", "admin-password", "1.1.1.1", ""); err != nil {
		t.Fatalf("original password, case-insensitive email: %v", err)
	}
}

func TestBootstrapRequiresCredentials(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatal(r)
		}
	}()
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.repo.Writer().ExecContext(ctx, "DELETE FROM audit_log; DELETE FROM sessions; DELETE FROM invites; DELETE FROM users")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.cfg.AdminPassword = ""
	if _, err := e.svc.Bootstrap(ctx); !errors.Is(err, ErrBootstrapCredentials) || !strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Fatalf("want missing ADMIN_PASSWORD, got %v", err)
	}
}

func TestInviteFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inv, err := e.svc.CreateUser(ctx, e.admin, "Ann@Example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.User.Status != domain.UserInvited || !strings.HasPrefix(inv.URL, "http://127.0.0.1:8082/invite/") {
		t.Fatalf("bad invite %+v", inv)
	}
	if _, err := e.svc.CreateUser(ctx, e.admin, "ann@example.COM", ""); err == nil {
		t.Fatal("duplicate email in another case must fail")
	} else {
		wantCode(t, err, domain.CodeEmailTaken)
	}
	secret := inv.URL[strings.LastIndex(inv.URL, "/")+1:]

	if _, err := e.svc.RedeemInvite(ctx, secret, "short", ""); err == nil {
		t.Fatal("short password must fail")
	} else {
		wantCode(t, err, domain.CodePasswordShort)
	}
	if u, err := e.svc.InviteUser(ctx, secret); err != nil || u.Email != "Ann@Example.com" {
		t.Fatalf("invite lookup: %v", err)
	}
	u, err := e.svc.RedeemInvite(ctx, secret, "long-enough-pw", "")
	if err != nil || u.Status != domain.UserActive {
		t.Fatalf("redeem: %v", err)
	}
	if _, err := e.svc.RedeemInvite(ctx, secret, "long-enough-pw", ""); err == nil {
		t.Fatal("used invite must fail")
	} else {
		wantCode(t, err, domain.CodeInviteInvalid)
	}
	if _, err := e.svc.Login(ctx, "ann@example.com", "long-enough-pw", "1.1.1.1", "ua"); err != nil {
		t.Fatal(err)
	}
}

func TestInviteExpiresAndReissueRevokes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inv, _ := e.svc.CreateUser(ctx, e.admin, "bob@example.com", domain.RoleUser)
	old := inv.URL[strings.LastIndex(inv.URL, "/")+1:]
	again, err := e.svc.ReissueInvite(ctx, e.admin, inv.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RedeemInvite(ctx, old, "long-enough-pw", ""); err == nil {
		t.Fatal("previous link must stop working after reissue")
	}
	fresh := again.URL[strings.LastIndex(again.URL, "/")+1:]
	e.clock.Advance(InviteTTL + time.Second)
	if _, err := e.svc.RedeemInvite(ctx, fresh, "long-enough-pw", ""); err == nil {
		t.Fatal("expired link must fail")
	} else {
		wantCode(t, err, domain.CodeInviteInvalid)
	}
}

func TestLoginErrorsAreIndistinguishable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err1 := e.svc.Login(ctx, "admin@pp.test", "wrong-password", "2.2.2.2", "")
	_, err2 := e.svc.Login(ctx, "nobody@pp.test", "wrong-password", "2.2.2.2", "")
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Fatalf("errors differ: %v / %v", err1, err2)
	}
	invited, _ := e.svc.CreateUser(ctx, e.admin, "inv@pp.test", "")
	_, err3 := e.svc.Login(ctx, invited.User.Email, "", "2.2.2.2", "")
	if err3 == nil || err3.Error() != err1.Error() {
		t.Fatalf("invited user error differs: %v", err3)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for range FailedLoginsPerIP {
		if _, err := e.svc.Login(ctx, "admin@pp.test", "nope-nope-nope", "3.3.3.3", ""); !errors.Is(err, domain.ErrBadCredentials) {
			t.Fatalf("want bad credentials, got %v", err)
		}
	}
	_, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "3.3.3.3", "")
	if de := domain.AsError(err); de == nil || de.Kind != domain.KindRateLimited || de.RetryAfter <= 0 {
		t.Fatalf("11th attempt must be rate limited even with the right password, got %v", err)
	}
	if _, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "4.4.4.4", ""); err != nil {
		t.Fatalf("other IPs unaffected: %v", err)
	}
	e.clock.Advance(FailedLoginsWindow)
	if _, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "3.3.3.3", ""); err != nil {
		t.Fatalf("window expired: %v", err)
	}
}

func TestSessionsSlideExpireAndLogout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "5.5.5.5", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateSession(ctx, res.Secret, "5.5.5.5"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(SessionTTL - time.Hour)
	if _, _, err := e.svc.AuthenticateSession(ctx, res.Secret, ""); err != nil {
		t.Fatalf("activity must extend the session: %v", err)
	}
	e.clock.Advance(SessionTTL - time.Hour)
	if _, _, err := e.svc.AuthenticateSession(ctx, res.Secret, ""); err != nil {
		t.Fatalf("session should have slid: %v", err)
	}
	e.clock.Advance(SessionTTL + time.Second)
	if _, _, err := e.svc.AuthenticateSession(ctx, res.Secret, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("idle session must expire: %v", err)
	}
	res, _ = e.svc.Login(ctx, "admin@pp.test", "admin-password", "5.5.5.5", "ua")
	if err := e.svc.Logout(ctx, res.Secret); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateSession(ctx, res.Secret, ""); err == nil {
		t.Fatal("logged out session must not work")
	}
	if _, _, err := e.svc.AuthenticateSession(ctx, "", ""); err == nil {
		t.Fatal("empty cookie")
	}
}

func TestBlockingStopsSessionsAndTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("carol@pp.test")
	login, _ := e.svc.Login(ctx, "carol@pp.test", "user-password-1", "6.6.6.6", "")
	tok, err := e.svc.CreateToken(ctx, u, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, tok.Value, "6.6.6.6"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AuthenticateSession(ctx, login.Secret, ""); err == nil {
		t.Fatal("blocked user's session must end")
	}
	if _, err := e.svc.AuthenticateToken(ctx, tok.Value, "6.6.6.6"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("blocked user's token must fail: %v", err)
	}
	if _, err := e.svc.SetUserBlocked(ctx, e.admin, e.admin.User.ID, true); err == nil {
		t.Fatal("admin cannot block themselves")
	}
	if _, err := e.svc.SetUserBlocked(ctx, u, e.admin.User.ID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("users cannot use admin functions")
	}
	unblocked, err := e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, false)
	if err != nil || unblocked.Status != domain.UserActive {
		t.Fatalf("unblock: %v %v", unblocked.Status, err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, tok.Value, "6.6.6.6"); err != nil {
		t.Fatalf("token works again after unblock: %v", err)
	}
}

func TestTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("dave@pp.test")
	other := e.user("eve@pp.test")
	tok, err := e.svc.CreateToken(ctx, u, "ci", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.Value) < 43 || tok.Token.Prefix != tok.Value[:9] {
		t.Fatalf("bad token %q", tok.Value)
	}
	list, _ := e.svc.ListTokens(ctx, u)
	if len(list) != 1 || list[0].Name != "ci" {
		t.Fatalf("list %+v", list)
	}
	if err := e.svc.RevokeToken(ctx, other, tok.Token.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoking another user's token must be not found: %v", err)
	}
	e.clock.Advance(2 * time.Hour)
	if _, err := e.svc.AuthenticateToken(ctx, tok.Value, "7.7.7.7"); err == nil {
		t.Fatal("expired token must fail")
	}
	tok2, _ := e.svc.CreateToken(ctx, u, "cli", 0)
	if err := e.svc.RevokeToken(ctx, u, tok2.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateToken(ctx, tok2.Value, "7.7.7.7"); err == nil {
		t.Fatal("revoked token must fail")
	}
	for _, bad := range []string{"", "garbage", "pp_" + strings.Repeat("x", 200)} {
		if _, err := e.svc.AuthenticateToken(ctx, bad, "8.8.8.8"); err == nil {
			t.Fatalf("token %q must fail", bad)
		}
	}
	if _, err := e.svc.CreateToken(ctx, u, "", 0); err == nil {
		t.Fatal("empty name must fail")
	}
}

func TestTokenBruteForceIsLimited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for range FailedTokensPerIP {
		_, _ = e.svc.AuthenticateToken(ctx, "pp_wrong", "9.9.9.9")
	}
	_, err := e.svc.AuthenticateToken(ctx, "pp_wrong", "9.9.9.9")
	if de := domain.AsError(err); de.Kind != domain.KindRateLimited {
		t.Fatalf("want rate limited, got %v", err)
	}
}

func TestSettingsOverrideLimitsWithoutRestart(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ReservedSlugs = []string{"envslug"} })
	ctx := context.Background()
	u := e.user("frank@pp.test")
	one := int64(1)
	if _, err := e.svc.UpdateSettings(ctx, u, SettingsUpdate{MaxSitesPerUser: &one}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("settings are admin only")
	}
	st, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{MaxSitesPerUser: &one, ReservedSlugs: &[]string{"Promo", "sale"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Limits.MaxSitesPerUser != 1 || !st.Overridden[SettingMaxSitesPerUser] {
		t.Fatalf("settings %+v", st)
	}
	if _, err := e.svc.CreateSite(ctx, u, "one", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSite(ctx, u, "two", ""); err == nil {
		t.Fatal("new limit must apply at once")
	} else {
		wantCode(t, err, domain.CodeSiteLimit)
	}
	for _, slug := range []string{"promo", "sale", "envslug", "api", "www"} {
		if _, err := e.svc.CreateSite(ctx, e.admin, "x", slug); err == nil {
			t.Fatalf("slug %s must be reserved", slug)
		}
	}
	keepOne := int64(1)
	if _, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{KeepReleases: &keepOne}); err == nil {
		t.Fatal("keep_releases below 2 must fail")
	}
	big := int64(1 << 40)
	if _, err := e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{MaxFileBytes: &big}); err == nil {
		t.Fatal("file limit above site limit must fail")
	}
	st, err = e.svc.UpdateSettings(ctx, e.admin, SettingsUpdate{Reset: []string{SettingMaxSitesPerUser}})
	if err != nil || st.Limits.MaxSitesPerUser != config.DefaultMaxSitesPerUser {
		t.Fatalf("reset: %v %+v", err, st.Limits)
	}
}

func TestAdminReads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("reader@pp.test")
	_, _ = e.svc.CreateSite(ctx, u, "s", "")
	users, total, err := e.svc.ListUsers(ctx, e.admin, UserFilter{Query: "reader"})
	if err != nil || total != 1 || users[0].SiteCount != 1 {
		t.Fatalf("list users: %v %d", err, total)
	}
	if _, _, err := e.svc.ListUsers(ctx, u, UserFilter{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("list users is admin only")
	}
	if got, err := e.svc.GetUser(ctx, e.admin, u.User.ID); err != nil || got.Email != "reader@pp.test" {
		t.Fatal(err)
	}
	if _, err := e.svc.GetUser(ctx, u, u.User.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("get user is admin only")
	}
	st, err := e.svc.GetSettings(ctx, e.admin)
	if err != nil || len(st.Builtin) == 0 || st.Limits.MaxFiles != config.DefaultMaxFiles {
		t.Fatalf("settings: %v", err)
	}
	if _, err := e.svc.GetSettings(ctx, u); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("settings are admin only")
	}
	acts, err := e.svc.ListAuditActions(ctx, e.admin)
	if err != nil || len(acts) == 0 {
		t.Fatal(err)
	}
	if _, err := e.svc.ListAuditActions(ctx, u); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("audit actions are admin only")
	}
	me, lim, n, err := e.svc.Me(ctx, u)
	if err != nil || me.ID != u.User.ID || n != 1 || lim.MaxSitesPerUser != 10 {
		t.Fatalf("me: %v", err)
	}
	if err := e.svc.SetLang(ctx, u, "en"); err != nil {
		t.Fatal(err)
	}
	if e.svc.Config().BaseDomain != testBase || e.svc.APIURL() != "https://api.pp.test" || e.svc.Ping(ctx) != nil {
		t.Fatal("accessors")
	}
	if _, err := e.svc.ReissueInvite(ctx, u, u.User.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("reissue is admin only")
	}
	_, _ = e.svc.SetUserBlocked(ctx, e.admin, u.User.ID, true)
	if _, err := e.svc.ReissueInvite(ctx, e.admin, u.User.ID); err == nil {
		t.Fatal("blocked users get no invites")
	}
	if _, err := e.svc.CreateUser(ctx, e.admin, "x@pp.test", "root"); err == nil {
		t.Fatal("unknown role")
	}
	if _, err := e.svc.InviteUser(ctx, "nope"); err == nil {
		t.Fatal("unknown invite")
	}
}
