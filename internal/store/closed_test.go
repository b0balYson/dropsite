package store

import (
	"context"
	"errors"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// TestClosedDatabaseErrors checks that driver failures surface as wrapped
// errors and are never mistaken for ErrNotFound or a domain conflict.
func TestClosedDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	must(t, db.Close())

	h := host("x."+base, "s", domain.HostSlug)
	calls := map[string]func() error{
		"Ping":            func() error { return db.Ping(ctx) },
		"CountUsers":      func() error { _, err := db.CountUsers(ctx); return err },
		"CreateUser":      func() error { _, err := db.CreateUser(ctx, domain.User{}); return err },
		"GetUser":         func() error { _, err := db.GetUser(ctx, 1); return err },
		"GetUserByEmail":  func() error { _, err := db.GetUserByEmail(ctx, "a"); return err },
		"ListUsers":       func() error { _, _, err := db.ListUsers(ctx, UserFilter{}); return err },
		"SetUserStatus":   func() error { return db.SetUserStatus(ctx, 1, domain.UserActive, t0) },
		"CreateInvite":    func() error { _, err := db.CreateInvite(ctx, domain.Invite{}); return err },
		"GetInviteByHash": func() error { _, err := db.GetInviteByHash(ctx, nil); return err },
		"RedeemInvite":    func() error { _, err := db.RedeemInvite(ctx, 1, "", t0); return err },
		"DeleteExpiredInvites": func() error {
			_, err := db.DeleteExpiredInvites(ctx, t0)
			return err
		},
		"CreateSession":         func() error { return db.CreateSession(ctx, domain.Session{}) },
		"GetSession":            func() error { _, err := db.GetSession(ctx, nil); return err },
		"TouchSession":          func() error { return db.TouchSession(ctx, nil, t0, t0) },
		"DeleteSession":         func() error { return db.DeleteSession(ctx, nil) },
		"DeleteUserSessions":    func() error { return db.DeleteUserSessions(ctx, 1) },
		"DeleteExpiredSessions": func() error { _, err := db.DeleteExpiredSessions(ctx, t0); return err },
		"CreateToken":           func() error { _, err := db.CreateToken(ctx, domain.Token{}); return err },
		"GetTokenByHash":        func() error { _, err := db.GetTokenByHash(ctx, nil); return err },
		"ListTokens":            func() error { _, err := db.ListTokens(ctx, 1); return err },
		"RevokeToken":           func() error { return db.RevokeToken(ctx, 1, 1, t0) },
		"TouchToken":            func() error { return db.TouchToken(ctx, 1, t0) },
		"SiteIDExists":          func() error { _, err := db.SiteIDExists(ctx, "s"); return err },
		"CountActiveSites":      func() error { _, err := db.CountActiveSites(ctx, 1); return err },
		"CreateSite":            func() error { return db.CreateSite(ctx, domain.Site{}, nil) },
		"GetSite":               func() error { _, err := db.GetSite(ctx, "s"); return err },
		"FindSiteBySlug":        func() error { _, err := db.FindSiteBySlug(ctx, "s"); return err },
		"ListSites":             func() error { _, _, err := db.ListSites(ctx, SiteFilter{}); return err },
		"UpdateSiteTitle":       func() error { return db.UpdateSiteTitle(ctx, "s", "t", t0) },
		"SetSlugHost":           func() error { _, err := db.SetSlugHost(ctx, "s", &h, t0); return err },
		"SetApexHost":           func() error { _, err := db.SetApexHost(ctx, "s", h, t0); return err },
		"ClearApexHost":         func() error { _, err := db.ClearApexHost(ctx, "s", t0); return err },
		"TransferSite":          func() error { return db.TransferSite(ctx, "s", 1, t0) },
		"SoftDeleteSite":        func() error { _, err := db.SoftDeleteSite(ctx, "s", t0); return err },
		"ListHostnames":         func() error { _, err := db.ListHostnames(ctx, "s"); return err },
		"ActiveLayout":          func() error { _, err := db.ActiveLayout(ctx); return err },
		"ListSitesToPurge":      func() error { _, err := db.ListSitesToPurge(ctx, t0); return err },
		"MarkSitePurged":        func() error { return db.MarkSitePurged(ctx, "s", t0) },
		"InsertDeployAndActivate": func() error {
			return db.InsertDeployAndActivate(ctx, domain.Deploy{SiteID: "s"}, t0)
		},
		"SetCurrentDeploy":  func() error { return db.SetCurrentDeploy(ctx, "s", "d", t0) },
		"GetDeploy":         func() error { _, err := db.GetDeploy(ctx, "s", "d"); return err },
		"ListDeploys":       func() error { _, err := db.ListDeploys(ctx, "s"); return err },
		"MarkDeploysPruned": func() error { return db.MarkDeploysPruned(ctx, "s", []string{"d"}) },
		"InsertAudit":       func() error { return db.InsertAudit(ctx, domain.AuditEntry{}) },
		"ListAudit":         func() error { _, _, err := db.ListAudit(ctx, AuditFilter{}); return err },
		"ListAuditActions":  func() error { _, err := db.ListAuditActions(ctx); return err },
		"GetSettings":       func() error { _, err := db.GetSettings(ctx); return err },
		"SetSetting":        func() error { return db.SetSetting(ctx, "k", "v") },
		"DeleteSetting":     func() error { return db.DeleteSetting(ctx, "k") },
	}
	for name, call := range calls {
		err := call()
		var de *domain.Error
		if err == nil || errors.As(err, &de) {
			t.Errorf("%s: want a wrapped driver error, got %v", name, err)
		}
	}
}
