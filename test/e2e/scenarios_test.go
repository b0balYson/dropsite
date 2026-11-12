//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStage1 walks through the stage-1 acceptance criteria against the real
// stack. Subtests share state and run in order.
func TestStage1(t *testing.T) {
	s := up(t)

	adminUI := newUI(t)
	adminUI.login(adminEmail, adminPass)
	admin := &api{s: s, token: adminUI.issueToken("e2e-admin")}

	ann := createUser(t, s, admin, "ann@dropsite.localhost")
	bob := createUser(t, s, admin, "bob@dropsite.localhost")

	var siteID, siteHost string
	t.Run("publish over the API and serve over HTTPS", func(t *testing.T) {
		st, body := ann.json(http.MethodPost, "/sites", map[string]any{"title": "Promo"})
		must(t, st, http.StatusCreated, body, "create site")
		siteID = str(body, "id")
		siteHost = hostOf(str(body, "url"))
		if siteHost != siteID+"."+baseDomain {
			t.Fatalf("default host %q", siteHost)
		}
		st, body = ann.deploy(siteID, makeZip(t, site(map[string]string{
			"dist/index.html":               "<h1>v1</h1>",
			"dist/about.html":               "about page",
			"dist/404.html":                 "custom not found",
			"dist/common.txt":               "same in every release",
			"dist/.env":                     "SECRET=1",
			"dist/_next/x.js":               "next chunk",
			"dist/_form/x.txt":              "must stay hidden",
			"dist/.well-known/security.txt": "Contact: x",
		})...))
		must(t, st, http.StatusCreated, body, "deploy v1")
		if str(body, "deploy_id") == "" || str(body, "url") == "" {
			t.Fatalf("deploy response %v", body)
		}
		s.expectBody(siteHost, "/", 200, "<h1>v1</h1>")
	})

	t.Run("serving rules", func(t *testing.T) { servingRules(t, s, siteHost) })

	t.Run("slug", func(t *testing.T) {
		st, body := ann.json(http.MethodPatch, "/sites/"+siteID, map[string]any{"slug": "promo-may"})
		must(t, st, http.StatusOK, body, "set slug")
		s.expectBody("promo-may."+baseDomain, "/", 200, "<h1>v1</h1>")
		s.expectBody(siteHost, "/", 200, "<h1>v1</h1>")
		st, body = bob.json(http.MethodPost, "/sites", map[string]any{"title": "x", "slug": "promo-may"})
		if st != http.StatusConflict || errCode(body) != "slug_taken" {
			t.Fatalf("taken slug: %d %v", st, body)
		}
		st, body = bob.json(http.MethodPost, "/sites", map[string]any{"title": "x", "slug": "admin"})
		if st != http.StatusBadRequest || errCode(body) != "slug_reserved" {
			t.Fatalf("reserved slug: %d %v", st, body)
		}
	})

	t.Run("isolation between users", func(t *testing.T) {
		missing, _ := bob.json(http.MethodGet, "/sites/zzzzzzzzzz", nil)
		for _, call := range []func() (int, map[string]any){
			func() (int, map[string]any) { return bob.json(http.MethodGet, "/sites/"+siteID, nil) },
			func() (int, map[string]any) { return bob.json(http.MethodGet, "/sites/promo-may", nil) },
			func() (int, map[string]any) { return bob.json(http.MethodDelete, "/sites/"+siteID, nil) },
			func() (int, map[string]any) {
				return bob.json(http.MethodPost, "/sites/"+siteID+"/rollback", map[string]any{})
			},
			func() (int, map[string]any) {
				return bob.deploy(siteID, makeZip(t, zipEntry{name: "index.html", body: "pwned"}))
			},
		} {
			if st, body := call(); st != missing || st != http.StatusNotFound {
				t.Fatalf("foreign site: %d %v", st, body)
			}
		}
		s.expectBody(siteHost, "/", 200, "<h1>v1</h1>")
	})

	t.Run("bad archives are rejected and the site stays", func(t *testing.T) {
		bomb := makeZip(t, zipEntry{name: "index.html", body: "x"}, zipEntry{name: "zeros.bin", body: strings.Repeat("\x00", 60<<20)})
		cases := map[string][]byte{
			"traversal": makeZip(t, zipEntry{name: "index.html", body: "x"}, zipEntry{name: "../../evil.html", body: "x"}),
			"symlink":   makeZip(t, zipEntry{name: "index.html", body: "x"}, zipEntry{name: "leak", body: "/etc/passwd", symlink: true}),
			"zip bomb":  bomb,
			"no index":  makeZip(t, zipEntry{name: "page.html", body: "x"}),
		}
		for name, archive := range cases {
			st, body := ann.deploy(siteID, archive)
			if st < 400 || st >= 500 || !strings.HasPrefix(errCode(body), "archive_") {
				t.Fatalf("%s: %d %v", name, st, body)
			}
		}
		s.expectBody(siteHost, "/", 200, "<h1>v1</h1>")
	})

	t.Run("host header spoofing", func(t *testing.T) { hostSpoofing(t, s, siteHost) })

	t.Run("cli publish and republish", func(t *testing.T) { cliDeploy(t, s, ann.token) })

	t.Run("rollback under load", func(t *testing.T) { rollbackUnderLoad(t, s, ann, siteID, siteHost) })

	t.Run("apex", func(t *testing.T) {
		st, body := admin.json(http.MethodPost, "/admin/sites/"+siteID+"/apex", nil)
		must(t, st, http.StatusOK, body, "bind apex")
		s.expectBody(baseDomain, "/common.txt", 200, "same in every release")
	})

	t.Run("openapi on the API host", func(t *testing.T) {
		st, _ := (&api{s: s}).json(http.MethodGet, "/openapi.json", nil)
		if st != http.StatusOK {
			t.Fatalf("openapi: %d", st)
		}
	})

	t.Run("visit statistics", func(t *testing.T) { visitStatistics(t, s, ann, bob, siteID, siteHost) })

	t.Run("site forms reach the webhook", func(t *testing.T) { siteForms(t, s, ann, siteID, siteHost) })

	t.Run("sites survive control outage and caddy restart", func(t *testing.T) {
		run(t, s.ctx, "stop", "control")
		defer run(t, s.ctx, "up", "-d", "--wait", "control")
		s.expectBody(siteHost, "/common.txt", 200, "same in every release")
		run(t, s.ctx, "restart", "caddy")
		s.expectBody(siteHost, "/common.txt", 200, "same in every release")
		s.expectBody("promo-may."+baseDomain, "/common.txt", 200, "same in every release")
	})
}

// createUser invites a user through the API, accepts the invite in the UI and
// issues an API token for them on the Connect AI page.
func createUser(t *testing.T, s *stack, admin *api, email string) *api {
	t.Helper()
	st, body := admin.json(http.MethodPost, "/admin/users", map[string]any{"email": email})
	must(t, st, http.StatusCreated, body, "create user")
	m := inviteRe.FindStringSubmatch(str(body, "invite_url"))
	if m == nil {
		t.Fatalf("invite url %v", body)
	}
	u := newUI(t)
	u.acceptInvite(m[1], "user-password-1") // signs the user in
	fresh := newUI(t)
	fresh.login(email, "user-password-1") // the new password works for a fresh login too
	return &api{s: s, token: u.issueToken("e2e")}
}

func servingRules(t *testing.T, s *stack, host string) {
	checks := []struct {
		path, contains string
		status         int
		cache          string
	}{
		{"/", "<h1>v1</h1>", 200, "no-cache"},
		{"/about", "about page", 200, "no-cache"},
		{"/missing", "custom not found", 404, ""},
		{"/common.txt", "same in every release", 200, "public, max-age=3600"},
		{"/.env", "", 404, ""},
		{"/a/%2eenv", "", 404, ""},
		{"/_form/x.txt", "", 404, ""},
		{"/_next/x.js", "next chunk", 200, ""},
		{"/.well-known/security.txt", "Contact", 200, ""},
	}
	for _, c := range checks {
		resp, body, err := s.get(host, c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if resp.StatusCode != c.status || !strings.Contains(body, c.contains) {
			t.Errorf("%s: status %d body %.80q", c.path, resp.StatusCode, body)
		}
		if strings.Contains(body, "SECRET=1") || strings.Contains(body, "must stay hidden") {
			t.Errorf("%s leaked hidden content", c.path)
		}
		if c.cache != "" && resp.Header.Get("Cache-Control") != c.cache {
			t.Errorf("%s: Cache-Control %q", c.path, resp.Header.Get("Cache-Control"))
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", c.path)
		}
	}
	req, _ := http.NewRequestWithContext(s.ctx, http.MethodGet, "http://"+host+":"+httpPort+"/", nil)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || !strings.HasPrefix(resp.Header.Get("Location"), "https://") {
		t.Errorf("http redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func hostSpoofing(t *testing.T, s *stack, host string) {
	requests := map[string]string{
		"Host ..":         "GET /index.html HTTP/1.1\r\nHost: ..\r\nConnection: close\r\n\r\n",
		"Host ../sites":   "GET / HTTP/1.1\r\nHost: ../sites\r\nConnection: close\r\n\r\n",
		"no Host, 1.0":    "GET /" + host + "/index.html HTTP/1.0\r\n\r\n",
		"Host mismatch":   "GET / HTTP/1.1\r\nHost: api." + baseDomain + "\r\nConnection: close\r\n\r\n",
		"Host uppercase":  "GET / HTTP/1.1\r\nHost: " + strings.ToUpper(host) + "\r\nConnection: close\r\n\r\n",
		"encoded Host":    "GET / HTTP/1.1\r\nHost: %2e%2e\r\nConnection: close\r\n\r\n",
		"other site path": "GET /../../by-host/" + host + "/index.html HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n",
	}
	for name, raw := range requests {
		resp, err := rawHTTP(host, raw)
		if err != nil {
			continue // a refused connection leaks nothing
		}
		if strings.Contains(resp, "<h1>v1</h1>") && name != "other site path" {
			t.Errorf("%s served site content:\n%.200s", name, resp)
		}
		if name == "other site path" && !strings.HasPrefix(resp, "HTTP/1.1 200") && !strings.HasPrefix(resp, "HTTP/1.1 404") && !strings.HasPrefix(resp, "HTTP/1.1 400") {
			t.Errorf("%s: unexpected %.80q", name, resp)
		}
	}
	if _, _, err := s.get("nosuchsite."+baseDomain, "/"); err == nil {
		t.Error("unknown host must fail the TLS handshake")
	}
}

func cliDeploy(t *testing.T, s *stack, token string) {
	bin := filepath.Join(t.TempDir(), "dropsite")
	build := exec.CommandContext(s.ctx, "go", "build", "-o", bin, "./cmd/dropsite")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cli: %v\n%s", err, out)
	}
	ca := filepath.Join(t.TempDir(), "root.crt")
	out, err := compose(s.ctx, "exec", "-T", "caddy", "cat", "/caddy/data/caddy/pki/authorities/local/root.crt").Output()
	if err != nil {
		t.Fatalf("read local CA: %v", err)
	}
	if err := os.WriteFile(ca, out, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "dist"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "dist", "index.html"), []byte("<h1>from cli</h1>"), 0o644)
	env := append(os.Environ(),
		"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"DROPSITE_SERVER=https://"+apiHost+":"+httpsPort,
		"DROPSITE_TOKEN="+token, "DROPSITE_CA_FILE="+ca)
	deploy := func() string {
		cmd := exec.CommandContext(s.ctx, bin, "deploy", filepath.Join(dir, "dist"))
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("dropsite deploy: %v\n%s", err, stderr.String())
		}
		return strings.TrimSpace(string(out))
	}
	first := deploy()
	second := deploy()
	if first == "" || first != second {
		t.Fatalf("republish must print the same URL: %q vs %q", first, second)
	}
	s.expectBody(hostOf(first), "/", 200, "from cli")
	if _, err := os.Stat(filepath.Join(dir, "dist", ".dropsite.json")); err != nil {
		t.Fatal("cli must remember the site")
	}
}

// rollbackUnderLoad publishes and rolls back while clients keep reading;
// no request may see an error or a 404 caused by a switch.
func rollbackUnderLoad(t *testing.T, s *stack, owner *api, siteID, host string) {
	var stop atomic.Bool
	var total, bad atomic.Int64
	var mu sync.Mutex
	var samples []string
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for _, p := range []string{"/common.txt", "/"} {
					resp, body, err := s.get(host, p)
					total.Add(1)
					if err != nil || resp.StatusCode != 200 || (p == "/common.txt" && body != "same in every release") {
						bad.Add(1)
						mu.Lock()
						if len(samples) < 5 {
							samples = append(samples, fmt.Sprintf("%s: %v %v", p, statusOf(resp), err))
						}
						mu.Unlock()
					}
				}
			}
		}()
	}
	for i := range 4 {
		st, body := owner.deploy(siteID, makeZip(t, site(map[string]string{
			"index.html": fmt.Sprintf("<h1>v%d</h1>", i+2), "common.txt": "same in every release",
		})...))
		must(t, st, http.StatusCreated, body, "deploy under load")
		time.Sleep(200 * time.Millisecond)
	}
	for range 4 { // v5 -> v4 -> v3 -> v2 -> v1: every step has a previous release
		st, body := owner.json(http.MethodPost, "/sites/"+siteID+"/rollback", map[string]any{})
		must(t, st, http.StatusOK, body, "rollback under load")
		time.Sleep(150 * time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()
	t.Logf("requests during switches: %d, unexpected: %d", total.Load(), bad.Load())
	if bad.Load() != 0 {
		t.Fatalf("%d unexpected responses, e.g. %v", bad.Load(), samples)
	}
	if total.Load() < 200 {
		t.Fatalf("load too small to be meaningful: %d", total.Load())
	}
}

func statusOf(r *http.Response) any {
	if r == nil {
		return nil
	}
	return r.StatusCode
}
