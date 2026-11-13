package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

// /me tells API clients (the CLI's MCP server) where the web interface is:
// the public address while public access is on, UI_URL otherwise.
func TestMeUIURLFollowsPublicAccess(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	ui := func() string { return decodeAs[meDTO](t, h.do(http.MethodGet, "/api/v1/me", tok, nil)).UIURL }
	if got := ui(); got != h.env.Svc.Config().UIURL {
		t.Fatalf("off: %q", got)
	}
	ctx := context.Background()
	if err := h.env.Svc.SetPublicAdmin(ctx, h.env.Admin, true, true, testenv.AdminPassword); err != nil {
		t.Fatal(err)
	}
	if got := ui(); got != "https://admin."+testenv.BaseDomain {
		t.Fatalf("on: %q", got)
	}
	if err := h.env.Svc.SetPublicAdmin(ctx, h.env.Admin, false, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := ui(); got != h.env.Svc.Config().UIURL {
		t.Fatalf("off again: %q", got)
	}
}
