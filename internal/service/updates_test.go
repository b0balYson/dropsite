package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b0balYson/dropsite/internal/config"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
	"github.com/b0balYson/dropsite/internal/updates"
)

// fakeReleases answers the GitHub release endpoint with a fixed tag.
type fakeReleases struct {
	tag   atomic.Value
	calls atomic.Int32
}

func (f *fakeReleases) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	body := `{"tag_name":"v` + f.tag.Load().(string) + `","prerelease":false,"draft":false}`
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(body))}, nil
}

// withRelease makes e's service run version and see latest as the newest
// release.
func withRelease(t *testing.T, e *env, version, latest string) *fakeReleases {
	t.Helper()
	rt := &fakeReleases{}
	rt.tag.Store(latest)
	c, err := updates.NewChecker(config.DefaultUpdateCheckURL, rt)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.version, e.svc.checker = version, c
	return rt
}

func (e *env) heartbeat() {
	e.t.Helper()
	dir := e.svc.updFiles.Dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	ts := strconv.FormatInt(e.clock.Now().Unix(), 10)
	if err := os.WriteFile(filepath.Join(dir, updates.HeartbeatFile), []byte(ts), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func TestUpdateCheckComparesVersions(t *testing.T) {
	cases := []struct {
		running, latest string
		available       bool
	}{
		{"0.4.0", "0.5.0", true},
		{"0.4.0", "0.4.0", false},
		{"0.5.0", "0.4.9", false},
		{"0.4.0", "0.10.0", true},
	}
	for _, tc := range cases {
		e := newEnv(t)
		withRelease(t, e, tc.running, tc.latest)
		ctx := context.Background()
		if err := e.svc.CheckUpdatesNow(ctx, e.admin); err != nil {
			t.Fatal(err)
		}
		v, u, ok := e.svc.AvailableUpdate(ctx)
		if ok != tc.available {
			t.Fatalf("%s vs %s: available %v", tc.running, tc.latest, ok)
		}
		if ok && (v != tc.latest || u != updates.ReleasePageBase+tc.latest) {
			t.Fatalf("got %s %s", v, u)
		}
		raw, _ := e.repo.GetSettings(ctx)
		if raw[SettingUpdateLatest] != tc.latest || raw[SettingUpdateCheckedAt] == "" || raw[SettingUpdateURL] == "" {
			t.Fatalf("settings %v", raw)
		}
	}
}

func TestUpdateCheckSkipsDevAndDisabled(t *testing.T) {
	e := newEnv(t)
	rt := withRelease(t, e, "dev+abc123", "9.9.9")
	ctx := context.Background()
	if err := e.svc.CheckUpdatesNow(ctx, e.admin); !errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("dev build: %v", err)
	}
	e.svc.RunUpdateCheck(ctx) // returns at once on a development build
	if _, _, ok := e.svc.AvailableUpdate(ctx); ok || rt.calls.Load() != 0 {
		t.Fatal("dev build compared")
	}
	off := newEnv(t, func(c *config.Config) { c.UpdateCheck = false })
	if off.svc.checker != nil {
		t.Fatal("UPDATE_CHECK=0 still builds a checker")
	}
	if err := off.svc.CheckUpdatesNow(ctx, off.admin); !errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestUpdateCheckNowIsAdminOnlyAndLimited(t *testing.T) {
	e := newEnv(t)
	withRelease(t, e, "0.4.0", "0.5.0")
	ctx := context.Background()
	if err := e.svc.CheckUpdatesNow(ctx, e.user("u@pp.test")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-admin: %v", err)
	}
	for i := 0; i < ManualChecksPerWindow; i++ {
		if err := e.svc.CheckUpdatesNow(ctx, e.admin); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.CheckUpdatesNow(ctx, e.admin); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("over the limit: %v", err)
	}
	// The assistant's refresh shares the same limit.
	if _, err := e.svc.GetUpdateFacts(ctx, Actor{User: e.admin.User, ViaMCP: true}, true); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("refresh over the limit: %v", err)
	}
}

func TestRequestUpdate(t *testing.T) {
	e := newEnv(t)
	withRelease(t, e, "0.4.0", "0.5.0")
	ctx := context.Background()
	if err := e.svc.CheckUpdatesNow(ctx, e.admin); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RequestUpdate(ctx, e.admin, "0.5.0", true); !errors.Is(err, ErrUpdateNoHelper) {
		t.Fatalf("without the helper: %v", err)
	}
	e.heartbeat()
	denied := map[string]Actor{
		"user":      e.user("u@pp.test"),
		"api token": {User: e.admin.User, TokenID: 3},
		"oauth":     {User: e.admin.User, GrantID: 4},
		"mcp":       {User: e.admin.User, ViaMCP: true},
	}
	for name, a := range denied {
		if err := e.svc.RequestUpdate(ctx, a, "0.5.0", true); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := e.svc.RequestUpdate(ctx, e.admin, "0.5.0", false); err == nil {
		t.Fatal("unconfirmed request accepted")
	}
	for _, v := range []string{"0.6.0", "0.4.0", "0.5.0\nadmin=1", "../0.5.0", ""} {
		if err := e.svc.RequestUpdate(ctx, e.admin, v, true); !errors.Is(err, ErrUpdateUnavailable) {
			t.Fatalf("%q: %v", v, err)
		}
	}
	if e.svc.updFiles.Pending() {
		t.Fatal("a refused request left a file")
	}
	if err := e.svc.RequestUpdate(ctx, e.admin, "0.5.0", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.svc.updFiles.Dir, updates.RequestFile))
	if err != nil {
		t.Fatal(err)
	}
	want := "version=0.5.0\nadmin=" + strconv.FormatInt(e.admin.User.ID, 10) + "\n"
	if string(b) != want {
		t.Fatalf("request %q, want %q", b, want)
	}
	if err := e.svc.RequestUpdate(ctx, e.admin, "0.5.0", true); !errors.Is(err, ErrUpdateBusy) {
		t.Fatalf("duplicate: %v", err)
	}
	rows, _, err := e.repo.ListAudit(ctx, store.AuditFilter{Action: "update.request", Page: store.Page{Limit: 10}})
	if err != nil || len(rows) != 1 || rows[0].Meta["to"] != "0.5.0" || rows[0].Meta["from"] != "0.4.0" {
		t.Fatalf("audit %v %v", rows, err)
	}
	info, err := e.svc.GetUpdateInfo(ctx, e.admin)
	if err != nil || !info.Pending || !info.Active || !info.HelperPresent {
		t.Fatalf("info %+v %v", info, err)
	}
}

func TestRequestUpdateWhileRunning(t *testing.T) {
	e := newEnv(t)
	withRelease(t, e, "0.4.0", "0.5.0")
	ctx := context.Background()
	if err := e.svc.CheckUpdatesNow(ctx, e.admin); err != nil {
		t.Fatal(err)
	}
	e.heartbeat()
	status := `{"state":"running","from":"0.4.0","to":"0.5.0","started_at":"` + e.clock.Now().Format("2006-01-02T15:04:05Z") + `"}`
	if err := os.WriteFile(filepath.Join(e.svc.updFiles.Dir, updates.StatusFile), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RequestUpdate(ctx, e.admin, "0.5.0", true); !errors.Is(err, ErrUpdateBusy) {
		t.Fatalf("while running: %v", err)
	}
}

func TestUpdateFacts(t *testing.T) {
	e := newEnv(t)
	rt := withRelease(t, e, "0.4.0", "0.5.0")
	ctx := context.Background()
	user := e.user("u@pp.test")
	f, err := e.svc.GetUpdateFacts(ctx, user, true)
	if err != nil || f.Available || f.Latest != "" || rt.calls.Load() != 0 {
		t.Fatalf("non-admin refresh must be ignored: %+v %v calls=%d", f, err, rt.calls.Load())
	}
	if _, err := e.svc.GetUpdateFacts(ctx, Actor{User: e.admin.User, ViaMCP: true}, true); err != nil {
		t.Fatal(err)
	}
	f, _ = e.svc.GetUpdateFacts(ctx, user, false)
	if !f.Available || f.Latest != "0.5.0" || f.IsAdmin || f.PanelURL != "" || f.HelperPresent {
		t.Fatalf("user facts %+v", f)
	}
	f, _ = e.svc.GetUpdateFacts(ctx, Actor{User: e.admin.User, ViaMCP: true}, false)
	if !f.IsAdmin || !strings.HasSuffix(f.PanelURL, UpdatesSettingsPath) {
		t.Fatalf("admin facts %+v", f)
	}
	if e.svc.updFiles.Pending() {
		t.Fatal("reading the facts wrote a request")
	}
	if _, err := e.svc.GetUpdateFacts(ctx, Actor{}, false); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("anonymous: %v", err)
	}
}
