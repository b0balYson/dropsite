// Package deploy holds checks of the shipped compose file and Caddyfile.
// They need Docker and are skipped when it is unavailable.
package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	caddyImage = "caddy:2.11.9"
	// Release images; DROPSITE_VERSION defaults to latest.
	controlImage  = "ghcr.io/b0balyson/dropsite-control:latest"
	wildcardImage = "ghcr.io/b0balyson/dropsite-caddy:latest"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("docker checks are skipped in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon is not reachable")
	}
}

func run(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out
}

type composeVolume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type composeService struct {
	Image       string            `json:"image"`
	PullPolicy  string            `json:"pull_policy"`
	Build       *composeBuild     `json:"build"`
	User        string            `json:"user"`
	ReadOnly    bool              `json:"read_only"`
	SecurityOpt []string          `json:"security_opt"`
	Command     []string          `json:"command"`
	Volumes     []composeVolume   `json:"volumes"`
	Environment map[string]string `json:"environment"`
}

type composeBuild struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
}

type composeFile struct {
	Services map[string]composeService `json:"services"`
	Volumes  map[string]any            `json:"volumes"`
}

func mount(s composeService, source string) (composeVolume, bool) {
	for _, v := range s.Volumes {
		if v.Type == "volume" && v.Source == source {
			return v, true
		}
	}
	return composeVolume{}, false
}

func TestComposeConfig(t *testing.T) {
	requireDocker(t)
	env := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(env, []byte("BASE_DOMAIN=example.com\nACME_EMAIL=a@example.com\n"+
		"ADMIN_EMAIL=admin@example.com\nADMIN_PASSWORD=secret-password\nCF_API_TOKEN=test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string][]string{
		"ondemand": {"-f", "docker-compose.yml"},
		"wildcard": {"-f", "docker-compose.yml", "-f", "compose.wildcard.yml"},
	} {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{"compose"}, files...), "--env-file", env, "config", "--format", "json")
			out := run(t, "docker", args...)
			checkCompose(t, out)
			checkImages(t, out, name == "wildcard")
		})
	}
	t.Run("pinned version", func(t *testing.T) {
		cmd := exec.Command("docker", "compose", "-f", "docker-compose.yml", "-f", "compose.wildcard.yml",
			"--env-file", env, "config", "--images")
		cmd.Env = append(os.Environ(), "DROPSITE_VERSION=1.4.2")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"ghcr.io/b0balyson/dropsite-control:1.4.2", "ghcr.io/b0balyson/dropsite-caddy:1.4.2"} {
			if !slices.Contains(strings.Fields(string(out)), want) {
				t.Errorf("images %q lack %s", out, want)
			}
		}
	})
}

// Verify the resolved configuration: control (and Caddy in wildcard mode)
// run the published release image, keep a build section for source builds,
// and are only pulled when missing, so "up" never replaces a pulled image
// with a build unless asked to.
func checkImages(t *testing.T, out []byte, wildcard bool) {
	t.Helper()
	var cf composeFile
	if err := json.Unmarshal(out, &cf); err != nil {
		t.Fatal(err)
	}
	control := cf.Services["control"]
	if control.Image != controlImage || control.PullPolicy != "missing" {
		t.Errorf("control: image=%q pull_policy=%q, want %q and missing", control.Image, control.PullPolicy, controlImage)
	}
	checkBuildContext(t, control.Build, "..", "deploy/Dockerfile")
	caddy := cf.Services["caddy"]
	if wildcard {
		if caddy.Image != wildcardImage || caddy.PullPolicy != "missing" {
			t.Errorf("wildcard Caddy: image=%q pull_policy=%q, want %q and missing", caddy.Image, caddy.PullPolicy, wildcardImage)
		}
		checkBuildContext(t, caddy.Build, "caddy", "Dockerfile")
	} else if caddy.Image != caddyImage || caddy.Build != nil {
		t.Errorf("ondemand Caddy must use the stock image, got image=%q build=%+v", caddy.Image, caddy.Build)
	}
}

func checkBuildContext(t *testing.T, build *composeBuild, contextDir, dockerfile string) {
	t.Helper()
	if build == nil {
		t.Fatal("missing local build configuration")
	}
	want, err := filepath.Abs(contextDir)
	if err != nil {
		t.Fatal(err)
	}
	if build.Context != want || build.Dockerfile != dockerfile {
		t.Errorf("build=%+v, want context=%q dockerfile=%q", build, want, dockerfile)
	}
	if _, err := os.Stat(filepath.Join(build.Context, build.Dockerfile)); err != nil {
		t.Errorf("Dockerfile must exist in the checkout: %v", err)
	}
}

func checkCompose(t *testing.T, out []byte) {
	t.Helper()
	var cf composeFile
	if err := json.Unmarshal(out, &cf); err != nil {
		t.Fatalf("parse compose config: %v", err)
	}
	for _, name := range []string{"sites", "state", "caddy_data", "logs"} {
		if _, ok := cf.Volumes[name]; !ok {
			t.Errorf("volume %q is missing", name)
		}
	}
	caddy, control, initSvc := cf.Services["caddy"], cf.Services["control"], cf.Services["caddy-init"]
	for name, s := range map[string]composeService{"caddy": caddy, "control": control} {
		if s.User == "" || strings.HasPrefix(s.User, "0") || !s.ReadOnly ||
			!slices.Contains(s.SecurityOpt, "no-new-privileges:true") {
			t.Errorf("%s: user=%q read_only=%v security_opt=%v", name, s.User, s.ReadOnly, s.SecurityOpt)
		}
	}
	if v, ok := mount(caddy, "logs"); !ok || v.ReadOnly || v.Target != "/logs" {
		t.Errorf("caddy must mount logs read-write at /logs, got %+v (found %v)", v, ok)
	}
	if v, ok := mount(control, "logs"); !ok || !v.ReadOnly || v.Target != "/logs" {
		t.Errorf("control must mount logs read-only at /logs, got %+v (found %v)", v, ok)
	}
	if v, ok := mount(caddy, "sites"); !ok || !v.ReadOnly {
		t.Errorf("caddy must mount sites read-only, got %+v", v)
	}
	if _, ok := mount(caddy, "state"); ok {
		t.Error("state must not be mounted into caddy")
	}
	if _, ok := mount(initSvc, "logs"); !ok || !slices.Contains(initSvc.Command, "/logs") {
		t.Errorf("caddy-init must hand /logs to caddy: command=%v", initSvc.Command)
	}
	checkAdminEnv(t, control.Environment)
}

// control listens on :8082 inside the container and checks the published
// ADMIN_BIND_IP itself, so both must reach its environment.
func checkAdminEnv(t *testing.T, env map[string]string) {
	t.Helper()
	if env["ADMIN_ADDR"] != ":8082" {
		t.Errorf("control ADMIN_ADDR=%q, want :8082", env["ADMIN_ADDR"])
	}
	if env["ADMIN_BIND_IP"] != "127.0.0.1" {
		t.Errorf("control ADMIN_BIND_IP=%q, want the default 127.0.0.1", env["ADMIN_BIND_IP"])
	}
	if _, ok := env["ALLOW_PUBLIC_ADMIN"]; !ok {
		t.Error("control must receive ALLOW_PUBLIC_ADMIN")
	}
}

// After the installer removes ADMIN_PASSWORD from .env, compose must still
// start control.
func TestComposeConfigWithoutAdminPassword(t *testing.T) {
	requireDocker(t)
	env := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(env, []byte("BASE_DOMAIN=example.com\nACME_EMAIL=a@example.com\n"+
		"ADMIN_EMAIL=admin@example.com\nADMIN_BIND_IP=10.0.0.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := run(t, "docker", "compose", "-f", "docker-compose.yml", "--env-file", env, "config", "--format", "json")
	var cf composeFile
	if err := json.Unmarshal(out, &cf); err != nil {
		t.Fatal(err)
	}
	control := cf.Services["control"]
	if control.Environment["ADMIN_PASSWORD"] != "" || control.Environment["ADMIN_BIND_IP"] != "10.0.0.5" {
		t.Errorf("control env: ADMIN_PASSWORD=%q ADMIN_BIND_IP=%q",
			control.Environment["ADMIN_PASSWORD"], control.Environment["ADMIN_BIND_IP"])
	}
}

func TestCaddyfileValidates(t *testing.T) {
	requireDocker(t)
	abs, err := filepath.Abs("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	// Wildcard mode is checked with the "internal" DNS provider, which needs no
	// plugin, so the stock image can adapt it.
	for mode, extra := range map[string][]string{
		"ondemand": nil,
		"wildcard": {"-e", "TLS_MODE=wildcard", "-e", "TLS_DNS_PROVIDER=internal"},
	} {
		t.Run(mode, func(t *testing.T) {
			args := append([]string{"run", "--rm", "-e", "BASE_DOMAIN=example.com", "-e", "ACME_EMAIL=a@example.com",
				"-e", "API_HOST=api.example.com", "-v", abs + ":/etc/caddy/Caddyfile:ro"}, extra...)
			args = append(args, caddyImage)
			run(t, "docker", append(args, "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")...)
			out := run(t, "docker", append(args, "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")...)
			checkLogging(t, out, mode == "wildcard")
		})
	}
}

func checkLogging(t *testing.T, out []byte, wildcard bool) {
	t.Helper()
	var cfg struct {
		Logging struct {
			Logs map[string]struct {
				Writer  map[string]any `json:"writer"`
				Include []string       `json:"include"`
			} `json:"logs"`
		} `json:"logging"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("parse adapted config: %v", err)
	}
	file, ok := cfg.Logging.Logs["sites"]
	if !ok || file.Writer["output"] != "file" || file.Writer["filename"] != "/logs/sites-access.log" ||
		file.Writer["roll_gzip"] != false || file.Writer["mode"] != "0644" {
		t.Errorf("sites logger must write an uncompressed, readable file: %+v", file)
	}
	stdout, ok := cfg.Logging.Logs["sites_stdout"]
	if !ok || stdout.Writer["output"] != "stdout" || !slices.Contains(stdout.Include, "http.log.access.sites") {
		t.Errorf("sites access log must also go to stdout: %+v", stdout)
	}
	if strings.Contains(string(out), `"api.example.com":["sites"]`) {
		t.Error("API requests must not be written to the sites access log")
	}
	if !strings.Contains(string(out), `"default_logger_name":"sites"`) {
		t.Error("the on-demand catch-all must log to the sites logger")
	}
	if wildcard && !strings.Contains(string(out), `"*.example.com":["sites"]`) {
		t.Error("the wildcard block must log to the sites logger")
	}
}
