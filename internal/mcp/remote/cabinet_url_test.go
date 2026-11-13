package remote

import (
	"context"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
)

// get_account and the forms tools tell the assistant where the web
// interface is: the public address while public access is on, UI_URL
// otherwise.
func TestCabinetURLFollowsPublicAccess(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	alice := r.env.User("alice@pp.test")
	cs := r.session(r.env.Token(alice, "assistant"))
	local := strings.TrimSuffix(r.env.Svc.Config().UIURL, "/")
	public := "https://admin." + testenv.BaseDomain

	out := mustCall(t, cs, "deploy_files", map[string]any{"title": "Coffee",
		"files": []map[string]string{{"path": "index.html", "content": "<p>coffee</p>"}}})
	site := out["site_id"].(string)
	check := func(base string) {
		t.Helper()
		if acc := mustCall(t, cs, "get_account", map[string]any{}); acc["cabinet_url"] != base {
			t.Fatalf("get_account cabinet_url %v, want %q", acc["cabinet_url"], base)
		}
		st := mustCall(t, cs, "get_form_settings", map[string]any{"site": site})
		if u, _ := st["cabinet_url"].(string); !strings.HasPrefix(u, base+"/sites/") {
			t.Fatalf("form settings cabinet_url %q, want under %q", u, base)
		}
	}
	check(local)
	if err := r.env.Svc.SetPublicAdmin(ctx, r.env.Admin, true, true, testenv.AdminPassword); err != nil {
		t.Fatal(err)
	}
	check(public)
	if err := r.env.Svc.SetPublicAdmin(ctx, r.env.Admin, false, false, ""); err != nil {
		t.Fatal(err)
	}
	check(local)
}
