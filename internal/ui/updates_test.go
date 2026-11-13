package ui

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/testenv"
	"github.com/b0balYson/dropsite/internal/updates"
)

// releaseRT answers the release endpoint with one tag.
type releaseRT string

func (tag releaseRT) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{"tag_name":"v` + string(tag) + `"}`
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(body))}, nil
}

// newUpdateHarness runs 0.4.0 with 0.5.0 published and already checked.
func newUpdateHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, testenv.WithRelease("0.4.0", releaseRT("0.5.0")))
	if err := h.env.Svc.CheckUpdatesNow(context.Background(), h.env.Admin); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) updateFile(name, body string) {
	h.t.Helper()
	dir := filepath.Join(h.env.StateDir, updates.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) heartbeat() {
	h.updateFile(updates.HeartbeatFile, strconv.FormatInt(h.env.Clock.Now().Unix(), 10))
}

func requestPath(h *harness) string {
	return filepath.Join(h.env.StateDir, updates.DirName, updates.RequestFile)
}

func TestUpdateBannerForAdminsOnly(t *testing.T) {
	h := newUpdateHarness(t)
	h.loginUser("user@pp.test")
	home := h.get("/")
	wantNotContains(t, home.body, "update-banner", "0.5.0")

	h.c = h.newClient()
	h.loginAdmin()
	home = h.get("/")
	wantContains(t, home.body, `class="update-banner"`, "0.5.0", updates.ReleasePageBase+"0.5.0", `href="/admin/settings#updates-h"`)
}

func TestUpdatesSectionAndManualCommand(t *testing.T) {
	h := newUpdateHarness(t)
	h.loginAdmin()
	p := h.get("/admin/settings")
	wantContains(t, p.body, `id="updates-h"`, "0.4.0", "0.5.0", "/admin/settings/update/check",
		"ssh &lt;server&gt; dropsite-update")
	// Without a heartbeat of the server helper there is no button.
	wantNotContains(t, p.body, `action="/admin/settings/update"`)

	h.heartbeat()
	p = h.get("/admin/settings")
	wantContains(t, p.body, `action="/admin/settings/update"`, `name="version" value="0.5.0"`, "dropsite-rollback",
		`name="confirm" value="1" required`)
}

func TestRequestUpdateFromSettings(t *testing.T) {
	h := newUpdateHarness(t)
	h.heartbeat()
	h.loginAdmin()

	// No CSRF token: refused, nothing written.
	p := h.post("/admin/settings/update", url.Values{"version": {"0.5.0"}, "confirm": {"1"}})
	wantStatus(t, p, http.StatusForbidden)
	// Without the confirmation: nothing written.
	p = h.follow(h.submit("/admin/settings", "/admin/settings/update", url.Values{"version": {"0.5.0"}, "_section": {"updates-h"}}))
	wantContains(t, p.body, "Отметьте")
	if _, err := os.Stat(requestPath(h)); err == nil {
		t.Fatal("request written without CSRF or confirmation")
	}

	p = h.submit("/admin/settings", "/admin/settings/update", url.Values{"version": {"0.5.0"}, "confirm": {"1"}, "_section": {"updates-h"}})
	wantStatus(t, p, http.StatusSeeOther)
	b, err := os.ReadFile(requestPath(h))
	if err != nil {
		t.Fatal(err)
	}
	if want := "version=0.5.0\nadmin=" + strconv.FormatInt(h.env.Admin.User.ID, 10) + "\n"; string(b) != want {
		t.Fatalf("request %q", b)
	}
	page := h.get("/admin/settings")
	wantContains(t, page.body, `data-update-poll="/admin/settings/update/status"`, "data-update-offline")
	wantNotContains(t, page.body, `action="/admin/settings/update"`)

	// A second request while one waits.
	p = h.follow(h.submit("/admin/settings", "/admin/settings/update", url.Values{"version": {"0.5.0"}, "confirm": {"1"}, "_section": {"updates-h"}}))
	wantContains(t, p.body, "уже ждёт или идёт")

	st := h.get("/admin/settings/update/status", "Accept", "application/json")
	wantContains(t, st.body, `"active":true`, `"state":"queued"`)
}

func TestRequestUpdateDeniedToUsers(t *testing.T) {
	h := newUpdateHarness(t)
	h.heartbeat()
	h.loginUser("user@pp.test")
	tok := h.token("/")
	for _, path := range []string{"/admin/settings/update", "/admin/settings/update/check"} {
		p := h.post(path, url.Values{"_csrf": {tok}, "version": {"0.5.0"}, "confirm": {"1"}})
		wantStatus(t, p, http.StatusNotFound)
	}
	wantStatus(t, h.get("/admin/settings/update/status"), http.StatusNotFound)
	if _, err := os.Stat(requestPath(h)); err == nil {
		t.Fatal("a user wrote an update request")
	}
}

func TestUpdateStatusRendering(t *testing.T) {
	h := newUpdateHarness(t)
	h.heartbeat()
	h.loginAdmin()
	now := h.env.Clock.Now().UTC().Format("2006-01-02T15:04:05Z")

	h.updateFile(updates.StatusFile, `{"state":"running","from":"0.4.0","to":"0.5.0","started_at":"`+now+`"}`)
	p := h.get("/admin/settings/update/status")
	wantContains(t, p.body, "Обновляется до 0.5.0", "data-update-poll")

	h.updateFile(updates.StatusFile, `{"state":"failed","from":"0.4.0","to":"0.5.0","started_at":"`+now+`","finished_at":"`+now+`","message":"images not found","log":["==> Backing up","error: <b>pull</b>"]}`)
	p = h.get("/admin/settings/update/status")
	wantContains(t, p.body, "Обновление до 0.5.0 не удалось", "images not found", "error: &lt;b&gt;pull&lt;/b&gt;")
	wantNotContains(t, p.body, "data-update-poll", "<b>pull</b>")

	h.updateFile(updates.StatusFile, `{"state":"done","from":"0.3.0","to":"0.4.0","started_at":"`+now+`","finished_at":"`+now+`"}`)
	p = h.get("/admin/settings/update/status")
	wantContains(t, p.body, "Обновлено до 0.4.0", "dropsite-rollback")
}

func TestCheckNowFromSettings(t *testing.T) {
	h := newUpdateHarness(t)
	h.loginAdmin()
	p := h.follow(h.submit("/admin/settings", "/admin/settings/update/check", url.Values{"_section": {"updates-h"}}))
	wantContains(t, p.body, "доступна версия 0.5.0")
}

// The public admin address uses the same handlers: an administrator signed
// in there may request the update, with the public origin only.
func TestRequestUpdateOnPublicAdmin(t *testing.T) {
	p := newPublicHarness(t, testenv.WithRelease("0.4.0", releaseRT("0.5.0")))
	if err := p.env.Svc.CheckUpdatesNow(context.Background(), p.env.Admin); err != nil {
		t.Fatal(err)
	}
	p.heartbeat()
	setPublic(t, p.harness, true)
	p.loginAdmin()
	form := func() url.Values { return url.Values{"version": {"0.5.0"}, "confirm": {"1"}} }
	bad := p.submit("/admin/settings", "/admin/settings/update", form(), "Origin", "https://docs."+testenv.BaseDomain)
	wantStatus(t, bad, http.StatusForbidden)
	if _, err := os.Stat(requestPath(p.harness)); err == nil {
		t.Fatal("cross-origin request written")
	}
	ok := p.submit("/admin/settings", "/admin/settings/update", form(), "Origin", "https://"+publicHost)
	wantStatus(t, ok, http.StatusSeeOther)
	if _, err := os.Stat(requestPath(p.harness)); err != nil {
		t.Fatal("request from the public admin not written")
	}
}
