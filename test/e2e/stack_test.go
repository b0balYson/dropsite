//go:build e2e

// Package e2e runs the whole stack (caddy + control) with docker compose and
// a local certificate authority, and walks through the stage-1 scenarios.
//
//	make e2e
package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	project    = "dropsite-e2e"
	baseDomain = "dropsite.localhost"
	httpsPort  = "18443"
	httpPort   = "18080"
	uiURL      = "http://127.0.0.1:18082"
	adminEmail = "admin@dropsite.localhost"
	adminPass  = "local-dev-password"
)

// repoRoot returns the repository root from this file's location.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// wildcardStand reports whether the stack runs in wildcard TLS mode
// (E2E_WILDCARD=1): compose.wildcard.yml on top, with the wildcard
// certificate signed by Caddy's local CA instead of a real DNS provider.
func wildcardStand() bool { return os.Getenv("E2E_WILDCARD") != "" }

func compose(ctx context.Context, args ...string) *exec.Cmd {
	root := repoRoot()
	base := []string{"compose", "-p", project,
		"-f", filepath.Join(root, "deploy", "docker-compose.yml"),
		"-f", filepath.Join(root, "deploy", "compose.local.yml"),
		"-f", filepath.Join(root, "test", "e2e", "compose.forms.yml")}
	if wildcardStand() {
		base = append(base, "-f", filepath.Join(root, "deploy", "compose.wildcard.yml"))
	}
	base = append(base, "--env-file", filepath.Join(root, "deploy", "local.env"))
	cmd := exec.CommandContext(ctx, "docker", append(base, args...)...)
	cmd.Dir = filepath.Join(root, "deploy")
	if wildcardStand() {
		// The "internal" provider never calls a DNS API; the token only
		// satisfies the override's required variable.
		cmd.Env = append(os.Environ(), "TLS_DNS_PROVIDER=internal", "CF_API_TOKEN=unused-by-internal-provider")
	}
	return cmd
}

func run(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, err := compose(ctx, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// stack brings the stack up once for the whole test binary.
type stack struct {
	t      *testing.T
	ctx    context.Context
	client *http.Client // HTTPS to sites and API through 127.0.0.1:18443
}

func up(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()
	if os.Getenv("E2E_KEEP") == "" {
		t.Cleanup(func() {
			if t.Failed() {
				logs, _ := compose(ctx, "logs", "--no-color", "--tail", "200").CombinedOutput()
				t.Logf("compose logs:\n%s", logs)
			}
			_ = compose(ctx, "down", "-v", "--remove-orphans").Run()
		})
	}
	_ = compose(ctx, "down", "-v", "--remove-orphans").Run()
	run(t, ctx, "up", "-d", "--build", "--wait", "--wait-timeout", "180")
	s := &stack{t: t, ctx: ctx, client: siteClient()}
	s.waitHTTP(uiURL+"/login", 60*time.Second)
	return s
}

// siteClient resolves every host to the local Caddy and trusts its internal CA
// (the CA is not installed anywhere; the test only checks routing, not trust).
func siteClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			if port == "443" || port == httpsPort {
				return dialer.DialContext(ctx, network, "127.0.0.1:"+httpsPort)
			}
			return dialer.DialContext(ctx, network, "127.0.0.1:"+httpPort)
		},
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local CA in tests
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func uiClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *stack) waitHTTP(url string, max time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx // test polling
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.t.Fatalf("%s did not come up in %v", url, max)
}

// get fetches https://host/path through Caddy. The first request to a new
// host waits for on-demand issuance, so callers retry handshake failures.
func (s *stack) get(host, path string, header ...string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "https://"+host+":"+httpsPort+path, nil)
	if err != nil {
		return nil, "", err
	}
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "Host" {
			req.Host = header[i+1]
			continue
		}
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b), nil
}

// eventually retries fetch until check passes (certificate issuance and
// symlink switches are immediate, but the first handshake takes a moment).
func (s *stack) eventually(what string, fn func() error) {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if last = fn(); last == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	s.t.Fatalf("%s: %v", what, last)
}

func (s *stack) expectBody(host, path string, status int, contains string) {
	s.t.Helper()
	s.eventually(fmt.Sprintf("GET https://%s%s", host, path), func() error {
		resp, body, err := s.get(host, path)
		if err != nil {
			return err
		}
		if resp.StatusCode != status || !strings.Contains(body, contains) {
			return fmt.Errorf("status %d, body %.120q", resp.StatusCode, body)
		}
		return nil
	})
}

// rawHTTP sends bytes over a TLS connection to Caddy with the given SNI and
// returns the raw response (for malformed Host headers net/http refuses to send).
func rawHTTP(sni, request string) (string, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", "127.0.0.1:"+httpsPort,
		&tls.Config{ServerName: sni, InsecureSkipVerify: true}) //nolint:gosec // local CA in tests
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(request)); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, conn)
	return buf.String(), nil
}
