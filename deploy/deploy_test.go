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

const caddyImage = "caddy:2.11.9"

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
	User        string          `json:"user"`
	ReadOnly    bool            `json:"read_only"`
	SecurityOpt []string        `json:"security_opt"`
	Command     []string        `json:"command"`
	Volumes     []composeVolume `json:"volumes"`
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
			checkCompose(t, run(t, "docker", args...))
		})
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
