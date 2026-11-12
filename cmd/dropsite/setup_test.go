package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/skills"
)

func TestLoginWithCode(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("login", "--server", e.srv.URL, "--code", strings.ToLower(goodCode))
	expectCode(t, r, 1) // the fake server wants the exact code
	r = e.run("login", "--server", e.srv.URL, "--code", goodCode)
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "Logged in", userEmail)
	if e.srv.tokenName != "dropsite CLI on test-host" {
		t.Fatalf("token name %q", e.srv.tokenName)
	}
	b, _ := os.ReadFile(e.configFile())
	var c config
	_ = json.Unmarshal(b, &c)
	if c.Token != goodToken {
		t.Fatalf("config %+v", c)
	}
	// The code works once; the error explains what to do.
	r = e.run("login", "--server", e.srv.URL, "--code", goodCode)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "connect_code_invalid", "Connect AI page")
	// Usage errors.
	expectCode(t, e.run("login", "--code", goodCode), 2)
	// The code may come from the environment (installers).
	e2 := newTestEnv(t)
	e2.env[envServer], e2.env[envCode] = e2.srv.URL, goodCode
	expectCode(t, e2.run("login"), 0)
	e3 := newTestEnv(t)
	e3.env[envCode] = goodCode
	expectCode(t, e3.run("login", "--server", "http://example.com", "--code", goodCode), 1)
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

type setupJSON struct {
	Integrations []integration `json:"integrations"`
	LoggedIn     bool          `json:"logged_in"`
}

func runSetup(t *testing.T, e *testEnv, args ...string) map[string]integration {
	t.Helper()
	r := e.run(append([]string{"setup", "--json"}, args...)...)
	expectCode(t, r, 0)
	var out setupJSON
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("setup json: %v\n%s", err, r.stdout)
	}
	m := map[string]integration{}
	for _, it := range out.Integrations {
		m[it.Name] = it
	}
	return m
}

func desktopFile(e *testEnv) string {
	return filepath.Join(e.home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
}

func TestSetupAllAssistants(t *testing.T) {
	e := newTestEnv(t)
	desktop := filepath.Dir(desktopFile(e))
	mkdirs(t, filepath.Join(e.home, ".claude", "skills", "other"), filepath.Join(e.home, ".codex"),
		filepath.Join(e.home, ".cursor"), desktop)
	existing := `{"theme":"dark","mcpServers":{"other":{"command":"x"}}}`
	if err := os.WriteFile(desktopFile(e), []byte(existing), 0o640); err != nil {
		t.Fatal(err)
	}

	m := runSetup(t, e)
	for _, name := range []string{"Claude Code", "Codex", "Cursor", "Claude Desktop"} {
		if m[name].Status != statusInstalled || !m[name].Restart {
			t.Fatalf("%s: %+v", name, m[name])
		}
	}
	for _, p := range []string{
		filepath.Join(e.home, ".claude", "skills", "dropsite-publish", "SKILL.md"),
		filepath.Join(e.home, ".agents", "skills", "dropsite-publish", "SKILL.md"),
		filepath.Join(e.home, ".cursor", "skills", "dropsite-publish", "SKILL.md"),
	} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != skills.Publish {
			t.Fatalf("%s: %v", p, err)
		}
	}
	var cfg map[string]any
	b, _ := os.ReadFile(desktopFile(e))
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	ds := servers["dropsite"].(map[string]any)
	if cfg["theme"] != "dark" || servers["other"] == nil || ds["command"] != "/opt/dropsite/bin/dropsite" ||
		ds["args"].([]any)[0] != "mcp" {
		t.Fatalf("desktop config: %s", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(desktopFile(e)); fi.Mode().Perm() != 0o640 {
			t.Fatalf("mode changed: %v", fi.Mode().Perm())
		}
	}

	// Running again changes nothing.
	m = runSetup(t, e)
	for name, it := range m {
		if it.Status != statusUnchanged || it.Restart {
			t.Fatalf("second run %s: %+v", name, it)
		}
	}
	b2, _ := os.ReadFile(desktopFile(e))
	if string(b2) != string(b) {
		t.Fatal("second run rewrote the desktop config")
	}
	// A moved binary updates the entry.
	e.exe = "/usr/local/bin/dropsite"
	if m = runSetup(t, e); m["Claude Desktop"].Status != statusUpdated {
		t.Fatalf("update: %+v", m["Claude Desktop"])
	}

	// Remove takes out only ours.
	m = runSetup(t, e, "--remove")
	for name, it := range m {
		if it.Status != statusRemoved {
			t.Fatalf("remove %s: %+v", name, it)
		}
	}
	if _, err := os.Stat(filepath.Join(e.home, ".claude", "skills", "dropsite-publish")); !os.IsNotExist(err) {
		t.Fatal("skill dir left behind")
	}
	if _, err := os.Stat(filepath.Join(e.home, ".claude", "skills", "other")); err != nil {
		t.Fatal("foreign skill removed")
	}
	b, _ = os.ReadFile(desktopFile(e))
	if !strings.Contains(string(b), `"other"`) || strings.Contains(string(b), `"dropsite"`) || !strings.Contains(string(b), `"theme"`) {
		t.Fatalf("after remove: %s", b)
	}
	m = runSetup(t, e, "--remove")
	if m["Cursor"].Status != statusAbsent || m["Claude Desktop"].Status != statusAbsent {
		t.Fatalf("second remove: %+v", m)
	}
}

func TestSetupNothingInstalledAndBrokenConfig(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("setup")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "not found on this computer")
	expectContains(t, "stderr", r.stderr, "not signed in")
	if m := runSetup(t, e, "--remove"); m["Codex"].Status != statusAbsent {
		t.Fatalf("%+v", m)
	}

	desktop := filepath.Dir(desktopFile(e))
	mkdirs(t, desktop, filepath.Join(e.home, ".cursor"))
	broken := []byte(`{"mcpServers": {`)
	if err := os.WriteFile(desktopFile(e), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	e.login()
	r = e.run("setup")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "FAILED", "Cursor", "connected", "Restart to apply: Cursor")
	expectContains(t, "stderr", r.stderr, "not valid JSON")
	if b, _ := os.ReadFile(desktopFile(e)); string(b) != string(broken) {
		t.Fatal("broken config was modified")
	}
	// mcpServers of the wrong type is also left alone.
	if err := os.WriteFile(desktopFile(e), []byte(`{"mcpServers":[1]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := runSetup(t, e); m["Claude Desktop"].Status != statusError {
		t.Fatalf("%+v", m["Claude Desktop"])
	}
	// A missing config file is created.
	_ = os.Remove(desktopFile(e))
	if m := runSetup(t, e); m["Claude Desktop"].Status != statusInstalled {
		t.Fatalf("%+v", m["Claude Desktop"])
	}
	// An empty file counts as an empty object.
	_ = os.WriteFile(desktopFile(e), nil, 0o600)
	if m := runSetup(t, e); m["Claude Desktop"].Status != statusInstalled {
		t.Fatalf("%+v", m["Claude Desktop"])
	}
}

func TestSetupPlatformsAndEnvDirs(t *testing.T) {
	e := newTestEnv(t)
	e.goos = "linux"
	custom := filepath.Join(e.home, "custom-claude")
	codex := filepath.Join(e.home, "codex-home")
	mkdirs(t, custom, codex)
	e.env["CLAUDE_CONFIG_DIR"], e.env["CODEX_HOME"] = custom, codex
	m := runSetup(t, e)
	if m["Claude Desktop"].Status != statusSkipped {
		t.Fatalf("linux desktop: %+v", m["Claude Desktop"])
	}
	if m["Claude Code"].Path != filepath.Join(custom, "skills", "dropsite-publish", "SKILL.md") || m["Claude Code"].Status != statusInstalled {
		t.Fatalf("claude dir: %+v", m["Claude Code"])
	}
	if m["Codex"].Path != filepath.Join(e.home, ".agents", "skills", "dropsite-publish", "SKILL.md") {
		t.Fatalf("codex path: %+v", m["Codex"])
	}

	w := newTestEnv(t)
	w.goos = "windows"
	appData := filepath.Join(w.home, "AppData", "Roaming")
	mkdirs(t, filepath.Join(appData, "Claude"))
	m = runSetup(t, w)
	if m["Claude Desktop"].Path != filepath.Join(appData, "Claude", "claude_desktop_config.json") || m["Claude Desktop"].Status != statusInstalled {
		t.Fatalf("windows: %+v", m["Claude Desktop"])
	}
	w.env["APPDATA"] = filepath.Join(w.home, "elsewhere")
	if m = runSetup(t, w); m["Claude Desktop"].Status != statusNotFound {
		t.Fatalf("APPDATA: %+v", m["Claude Desktop"])
	}
	expectCode(t, w.run("setup", "extra"), 2)
}

func TestJoinNames(t *testing.T) {
	for want, in := range map[string][]string{"": nil, "A": {"A"}, "A and B": {"A", "B"}, "A, B and C": {"A", "B", "C"}} {
		if got := joinNames(in); got != want {
			t.Errorf("%v: %q", in, got)
		}
	}
}
