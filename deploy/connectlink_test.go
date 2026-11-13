package deploy

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAPIAccessLogRedactsConnectCode runs Caddy with the Caddyfile and checks
// that the access log of the API host never keeps the code of a connect URL,
// neither in the path (/mcp/c/<code>, its metadata) nor percent-encoded in
// the resource parameter of the consent page.
func TestAPIAccessLogRedactsConnectCode(t *testing.T) {
	requireDocker(t)
	abs, err := filepath.Abs("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(run(t, "docker", "run", "-d", "--rm",
		"--add-host", "api.example.com:127.0.0.1",
		"-e", "ACME_EMAIL=a@example.com", "-e", "API_HOST=api.example.com", "-e", "ADMIN_HOST=admin.example.com",
		"-e", "CADDY_GLOBAL_EXTRA=local_certs", "-e", "XDG_DATA_HOME=/tmp", "-e", "XDG_CONFIG_HOME=/tmp",
		"-v", abs+":/etc/caddy/Caddyfile:ro", caddyImage,
		"caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })

	const code = "ABCDEFGHJKMNP"
	paths := []string{
		"https://api.example.com/mcp/c/" + code,
		"https://api.example.com/.well-known/oauth-protected-resource/mcp/c/" + strings.ToLower(code),
		"https://api.example.com/oauth/authorize?client_id=x&resource=https%3A%2F%2Fapi.example.com%2Fmcp%2Fc%2F" + code + "&state=s",
		"http://api.example.com/mcp/c/" + code,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, p := range paths {
		// Caddy needs a moment to start and to issue the local certificate.
		for {
			out, _ := exec.CommandContext(ctx, "docker", "exec", id, "wget", "-q", "-S", "--no-check-certificate",
				"-O", "/dev/null", p).CombinedOutput()
			if strings.Contains(string(out), "HTTP/") || ctx.Err() != nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	var logs string
	for ctx.Err() == nil {
		out, _ := exec.CommandContext(ctx, "docker", "logs", id).CombinedOutput()
		logs = string(out)
		if strings.Count(logs, "REDACTED") >= len(paths)-1 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if strings.Contains(strings.ToUpper(logs), code) {
		t.Fatalf("the access log keeps the connect code:\n%s", logs)
	}
	for _, want := range []string{`/mcp/c/REDACTED`, `%2Fmcp%2Fc%2FREDACTED`, `oauth-protected-resource/mcp/c/REDACTED`} {
		if !strings.Contains(logs, want) {
			t.Errorf("access log lacks %s:\n%s", want, logs)
		}
	}
}
