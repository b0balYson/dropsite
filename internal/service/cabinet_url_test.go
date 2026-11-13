package service

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Links to the web interface follow the switch at the moment they are
// made: invitations, the forms and proxy screens, the cabinet address.
func TestCabinetLinksFollowPublicAccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	public := "https://admin." + testBase
	local := strings.TrimSuffix(e.svc.cfg.UIURL, "/")
	n := 0
	check := func(base string) {
		t.Helper()
		if got := e.svc.CabinetURL(ctx); got != base {
			t.Fatalf("cabinet %q, want %q", got, base)
		}
		n++
		inv, err := e.svc.CreateUser(ctx, e.admin, "u"+string(rune('a'+n))+"@pp.test", domain.RoleUser)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(inv.URL, base+"/invite/") {
			t.Fatalf("invite %q, want under %q", inv.URL, base)
		}
		if got := e.svc.FormsPageURL("abc"); got != base+"/sites/abc/forms" {
			t.Fatalf("forms page %q", got)
		}
		if got := e.svc.ProxyPageURL("abc", "maps"); got != base+"/sites/abc/proxy#proxy-maps" {
			t.Fatalf("proxy page %q", got)
		}
	}
	check(local)
	if err := e.svc.SetPublicAdmin(ctx, e.admin, true, true, adminPass); err != nil {
		t.Fatal(err)
	}
	check(public)
	// An invitation made while public access was on keeps working after it
	// is turned off: the token is valid on both listeners.
	inv, err := e.svc.CreateUser(ctx, e.admin, "late@pp.test", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPublicAdmin(ctx, e.admin, false, false, ""); err != nil {
		t.Fatal(err)
	}
	check(local)
	secret := inv.URL[strings.LastIndex(inv.URL, "/")+1:]
	if _, err := e.svc.InviteUser(ctx, secret); err != nil {
		t.Fatalf("invitation made while on stopped working: %v", err)
	}
}
