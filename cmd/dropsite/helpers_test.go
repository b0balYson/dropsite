package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv is one isolated CLI invocation environment.
type testEnv struct {
	t        *testing.T
	srv      *fakeServer
	cfgDir   string
	env      map[string]string
	terminal bool
	stdin    string
	home     string
	goos     string
	exe      string
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return &testEnv{t: t, srv: newFakeServer(t), cfgDir: t.TempDir(), env: map[string]string{},
		home: t.TempDir(), goos: "darwin", exe: "/opt/dropsite/bin/dropsite"}
}

func (e *testEnv) run(args ...string) result {
	e.t.Helper()
	var out, errOut bytes.Buffer
	a := &app{
		stdout:     &out,
		stderr:     &errOut,
		stdin:      strings.NewReader(e.stdin),
		getenv:     func(k string) string { return e.env[k] },
		configDir:  func() (string, error) { return e.cfgDir, nil },
		isTerminal: func() bool { return e.terminal },
		hostname:   func() (string, error) { return "test-host", nil },
		homeDir:    func() (string, error) { return e.home, nil },
		goos:       e.goos,
		executable: func() (string, error) { return e.exe, nil },
	}
	code := a.run(context.Background(), args)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// login saves a working config via the login command.
func (e *testEnv) login() {
	e.t.Helper()
	r := e.run("login", "--server", e.srv.URL, "--token", goodToken)
	if r.code != 0 {
		e.t.Fatalf("login failed: %+v", r)
	}
}

func (e *testEnv) configFile() string {
	return filepath.Join(e.cfgDir, "dropsite", "config.json")
}

// siteDir creates a publishable directory with the given extra files.
func siteDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my-site")
	if files == nil {
		files = map[string]string{"index.html": "<h1>hi</h1>"}
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func expectCode(t *testing.T, r result, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit code %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.stdout, r.stderr)
	}
}

func expectContains(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Fatalf("%s does not contain %q:\n%s", what, sub, s)
		}
	}
}
