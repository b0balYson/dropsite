package deploy

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// checkPublicAdmin: the public admin listener is reached only by Caddy
// inside the compose network. control gets its address, Caddy the host
// name, and 8084 is never published to the host.
func checkPublicAdmin(t *testing.T, caddy, control composeService) {
	t.Helper()
	if control.Environment["PUBLIC_ADMIN_ADDR"] != ":8084" {
		t.Errorf("control PUBLIC_ADMIN_ADDR=%q, want :8084", control.Environment["PUBLIC_ADMIN_ADDR"])
	}
	if caddy.Environment["ADMIN_HOST"] != "admin.example.com" {
		t.Errorf("caddy ADMIN_HOST=%q, want admin.example.com", caddy.Environment["ADMIN_HOST"])
	}
	if len(control.Ports) == 0 {
		t.Error("control must publish the loopback admin UI")
	}
	for _, p := range control.Ports {
		if p.Target != 8082 {
			t.Errorf("control publishes port %d; only the loopback admin UI (8082) may be published", p.Target)
		}
	}
}

// checkAdminBlock: the public admin host is proxied to control:8084, is
// never written to an access log, and its certificate is only ever obtained
// on demand (through the permission endpoint), never at startup.
func checkAdminBlock(t *testing.T, out []byte) {
	t.Helper()
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Logs *struct {
						SkipHosts []string `json:"skip_hosts"`
					} `json:"logs"`
				} `json:"servers"`
			} `json:"http"`
			TLS struct {
				Automation struct {
					Policies []struct {
						Subjects []string `json:"subjects"`
						OnDemand bool     `json:"on_demand"`
					} `json:"policies"`
				} `json:"automation"`
			} `json:"tls"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	skipped := false
	for _, s := range cfg.Apps.HTTP.Servers {
		if s.Logs != nil && slices.Contains(s.Logs.SkipHosts, "admin.example.com") {
			skipped = true
		}
	}
	if !skipped {
		t.Error("the admin host must not be written to any access log")
	}
	for _, p := range cfg.Apps.TLS.Automation.Policies {
		if slices.Contains(p.Subjects, "admin.example.com") && !p.OnDemand {
			t.Errorf("the admin host must only get an on-demand certificate: %+v", p)
		}
	}
	for _, want := range []string{`"dial":"control:8084"`, `"Strict-Transport-Security":["max-age=31536000"]`,
		`"root":"/data/tls-hosts"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("adapted config lacks %s", want)
		}
	}
}

// TestCertificatePermission runs the shipped Caddyfile and asks its
// permission endpoint: a site address (by-host directory) and the public
// admin host (tls-hosts marker, present only while public access is on) are
// allowed, nothing else is.
func TestCertificatePermission(t *testing.T) {
	requireDocker(t)
	abs, err := filepath.Abs("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	// The sites volume is laid out inside the container (no bind mount, so
	// it works with any Docker host): what control writes to /data.
	id := strings.TrimSpace(string(run(t, "docker", "run", "-d", "--rm",
		"-e", "ACME_EMAIL=a@example.com", "-e", "API_HOST=api.example.com", "-e", "ADMIN_HOST=admin.example.com",
		"-e", "CADDY_GLOBAL_EXTRA=local_certs", "-e", "XDG_DATA_HOME=/tmp", "-e", "XDG_CONFIG_HOME=/tmp",
		"-v", abs+":/etc/caddy/Caddyfile:ro", caddyTestImage(t),
		"caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	run(t, "docker", "exec", id, "sh", "-c", "mkdir -p /data/by-host/docs.example.com /data/tls-hosts "+
		"/data/flags/noindex /data/flags/protected && touch /data/tls-hosts/admin.example.com")
	ask := func(name string) int {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			out, _ := exec.CommandContext(ctx, "docker", "exec", id, "wget", "-q", "-S", "-O", "/dev/null",
				"http://127.0.0.1:8090/?domain="+name).CombinedOutput()
			s := string(out)
			switch {
			case strings.Contains(s, " 200 "):
				return 200
			case strings.Contains(s, " 404 "):
				return 404
			}
			if ctx.Err() != nil {
				t.Fatalf("ask %s: %s", name, s)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	for name, want := range map[string]int{
		"docs.example.com":   200,
		"admin.example.com":  200,
		"other.example.com":  404,
		"tls-hosts":          404,
		"..":                 404,
		"%2e%2e":             404,
		"admin.example.com.": 404,
	} {
		if got := ask(name); got != want {
			t.Errorf("ask %q: %d, want %d", name, got, want)
		}
	}
	// Public access turned off: control removes the marker, nothing reloads.
	run(t, "docker", "exec", id, "rm", "/data/tls-hosts/admin.example.com")
	if got := ask("admin.example.com"); got != 404 {
		t.Errorf("admin host still allowed after the marker is gone: %d", got)
	}
}
