package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
	"github.com/b0balYson/dropsite/internal/updates"
)

type releaseTag string

func (tag releaseTag) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{"tag_name":"v` + string(tag) + `"}`))}, nil
}

// check_updates and the update facts of get_account match on both servers;
// neither server can write an update request.
func TestStdioCheckUpdatesMatchesRemote(t *testing.T) {
	r := newParityRig(t, testenv.WithRelease("0.4.0", releaseTag("0.5.0")))
	ctx := context.Background()
	if err := r.env.Svc.CheckUpdatesNow(ctx, r.env.Admin); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.env.StateDir, updates.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	beat := strconv.FormatInt(r.env.Clock.Now().Unix(), 10)
	if err := os.WriteFile(filepath.Join(dir, updates.HeartbeatFile), []byte(beat), 0o600); err != nil {
		t.Fatal(err)
	}

	userTok := r.env.Token(r.env.User("ann@pp.test"), "cli")
	u := same(t, r.stdio(userTok), r.web(userTok), "check_updates", map[string]any{"refresh": true})
	if u["update_available"] != true || u["latest_version"] != "0.5.0" || u["is_admin"] != false ||
		!strings.Contains(u["how_to_update"].(string), "administrator") || u["update_panel_url"] != nil {
		t.Fatalf("user: %v", u)
	}
	acc := same(t, r.stdio(userTok), r.web(userTok), "get_account", map[string]any{})
	if acc["update_available"] != true || acc["latest_version"] != "0.5.0" ||
		acc["release_notes_url"] != updates.ReleasePageBase+"0.5.0" {
		t.Fatalf("user account: %v", acc)
	}

	adminTok := r.env.Token(r.env.Admin, "cli")
	local, web := r.stdio(adminTok), r.web(adminTok)
	a := same(t, local, web, "check_updates", map[string]any{})
	if a["is_admin"] != true || !strings.HasSuffix(a["update_panel_url"].(string), "/admin/settings#updates-h") {
		t.Fatalf("admin: %v", a)
	}
	acc = same(t, local, web, "get_account", map[string]any{})
	if !strings.Contains(acc["summary"].(string), "0.5.0 is available") {
		t.Fatalf("admin account: %v", acc)
	}

	// refresh shares the limit of "Check now" (one check is used above).
	for i := 1; i < 5; i++ {
		cs := local
		if i%2 == 0 {
			cs = web
		}
		mustTool(t, cs, "check_updates", map[string]any{"refresh": true})
	}
	if _, msg := outputs(t, local, "check_updates", map[string]any{"refresh": true}); msg == "" {
		t.Fatal("stdio refresh not rate limited")
	}
	if _, msg := outputs(t, web, "check_updates", map[string]any{"refresh": true}); msg == "" {
		t.Fatal("remote refresh not rate limited")
	}
	if _, err := os.Stat(filepath.Join(dir, updates.RequestFile)); err == nil {
		t.Fatal("an MCP call wrote an update request")
	}
}
