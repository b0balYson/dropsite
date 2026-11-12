package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoginSavesConfigAndLaterCommandsWork(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("login", "--server", e.srv.URL+"/", "--token", goodToken)
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, userEmail)

	b, err := os.ReadFile(e.configFile())
	if err != nil {
		t.Fatal(err)
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Server != e.srv.URL || c.Token != goodToken {
		t.Fatalf("config = %+v", c)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(e.configFile())
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("config perm = %v, want 0600", fi.Mode().Perm())
		}
		di, _ := os.Stat(filepath.Dir(e.configFile()))
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("config dir perm = %v, want 0700", di.Mode().Perm())
		}
	}
	e.srv.addSite("Blog", "blog", 1)
	r = e.run("sites")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, "https://blog.sites.test", "Blog")
	if e.srv.lastClient == "" || e.srv.lastClient[:4] != "cli/" {
		t.Fatalf("X-Pushpage-Client = %q", e.srv.lastClient)
	}
}

func TestLoginInvalidTokenDoesNotSave(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("login", "--server", e.srv.URL, "--token", "pp_bad")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "rejected the token", "unauthorized")
	if _, err := os.Stat(e.configFile()); !os.IsNotExist(err) {
		t.Fatalf("config must not exist, stat err = %v", err)
	}
}

func TestLoginInvalidTokenKeepsExistingConfig(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	before, _ := os.ReadFile(e.configFile())
	expectCode(t, e.run("login", "--server", e.srv.URL, "--token", "pp_bad"), 1)
	after, _ := os.ReadFile(e.configFile())
	if string(before) != string(after) {
		t.Fatal("config changed after a failed login")
	}
}

func TestLoginTokenFromStdin(t *testing.T) {
	e := newTestEnv(t)
	e.stdin = goodToken + "\n"
	r := e.run("login", "--server", e.srv.URL, "--token", "-")
	expectCode(t, r, 0)
	expectContains(t, "stdout", r.stdout, userEmail)
}

func TestLoginEmptyStdin(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("login", "--server", e.srv.URL, "--token", "-")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "read token from stdin")
}

func TestLoginUsageErrors(t *testing.T) {
	e := newTestEnv(t)
	expectCode(t, e.run("login"), 2)
	expectCode(t, e.run("login", "--server", e.srv.URL), 2)
	expectCode(t, e.run("login", "--server", e.srv.URL, "--token", "   "), 2)
	expectCode(t, e.run("login", "--bogus"), 2)
	expectCode(t, e.run("login", "extra"), 2)
}

func TestLoginRefusesPlainHTTP(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("login", "--server", "http://example.com", "--token", goodToken)
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "plain http")
}

func TestLoginFromEnvWarnsAboutOverride(t *testing.T) {
	e := newTestEnv(t)
	e.env[envServer] = e.srv.URL
	e.env[envToken] = goodToken
	r := e.run("login")
	expectCode(t, r, 0)
	expectContains(t, "stderr", r.stderr, "PUSHPAGE_TOKEN is set")
}

func TestEnvOverridesConfig(t *testing.T) {
	e := newTestEnv(t)
	e.login()
	e.env[envToken] = "pp_bad"
	r := e.run("sites")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "pushpage login")

	e.env[envToken] = goodToken
	e.env[envServer] = "http://127.0.0.1:1" // unreachable: proves env server wins
	r = e.run("sites")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "cannot reach")

	delete(e.env, envServer)
	expectCode(t, e.run("sites"), 0)
}

func TestEnvOnlyWithoutConfig(t *testing.T) {
	e := newTestEnv(t)
	e.env[envServer] = e.srv.URL
	e.env[envToken] = goodToken
	expectCode(t, e.run("sites"), 0)
}

func TestCorruptConfig(t *testing.T) {
	e := newTestEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.configFile()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.configFile(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("sites")
	expectCode(t, r, 1)
	expectContains(t, "stderr", r.stderr, "not valid JSON", "login")
}

func TestNormalizeServer(t *testing.T) {
	ok := map[string]string{
		"api.example.com":                "https://api.example.com",
		"https://API.example.com/":       "https://api.example.com",
		"https://api.example.com/api/v1": "https://api.example.com",
		"http://localhost:8080/":         "http://localhost:8080",
		"http://127.0.0.1:9000":          "http://127.0.0.1:9000",
		"http://[::1]:9000":              "http://[::1]:9000",
		"https://example.com/pp/":        "https://example.com/pp",
	}
	for in, want := range ok {
		got, err := normalizeServer(in)
		if err != nil || got != want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "http://example.com", "ftp://example.com", "https://", "https://u:p@example.com",
		"https://example.com/?a=1", "https://exa mple.com"}
	for _, in := range bad {
		if got, err := normalizeServer(in); err == nil {
			t.Errorf("normalizeServer(%q) = %q, want error", in, got)
		}
	}
}

func TestConfigDirError(t *testing.T) {
	a := &app{configDir: func() (string, error) { return "", os.ErrNotExist }, getenv: func(string) string { return "" }}
	if _, err := a.connect(); err == nil {
		t.Fatal("expected error")
	}
	if _, err := a.saveConfig(config{}); err == nil {
		t.Fatal("expected error")
	}
}
