package oauth

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
)

// Claude Code (built-in metadata, loopback redirect on a run-time port)
// connecting with a connect URL: the request is valid and carries the code.
func TestClaudeCodeWithConnectURL(t *testing.T) {
	r := newRig(t, Options{Resolver: fakeResolver{addrs: addrs("127.0.0.1")}, AllowAddr: func(netip.Addr) bool { return false }})
	q := url.Values{"client_id": {"https://claude.ai/oauth/claude-code-client-metadata"}, "response_type": {"code"},
		"state": {"s"}, "code_challenge": {service.PKCEChallenge(strings.Repeat("v", 43))}, "code_challenge_method": {"S256"},
		"redirect_uri": {"http://localhost:53123/callback"}, "resource": {"https://api.pp.test/mcp/c/abcd-efgh-jkmnp"}}
	p, err := r.s.ValidateAuthorize(context.Background(), q, "1.1.1.1")
	if err != nil || p.Client.Name != "Claude Code" || p.LinkCode != "ABCDEFGHJKMNP" ||
		p.Request.RedirectURI != "http://localhost:53123/callback" || p.Request.Resource != "https://api.pp.test/mcp" {
		t.Fatalf("claude code with a connect URL: %+v %v", p, err)
	}
	q.Set("resource", "https://api.pp.test/mcp")
	if p, err := r.s.ValidateAuthorize(context.Background(), q, "1.1.1.1"); err != nil || p.LinkCode != "" {
		t.Fatalf("plain resource: %+v %v", p, err)
	}
}

func TestLinkResourceMetadata(t *testing.T) {
	r := newRig(t, Options{})
	resp, body := r.req(http.MethodGet, "/.well-known/oauth-protected-resource/mcp/c/abcd-efgh-jkmnp", "", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"resource":"https://api.pp.test/mcp/c/abcd-efgh-jkmnp"`) ||
		!strings.Contains(body, `"authorization_servers":["https://api.pp.test"]`) || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("link metadata: %d %v %s", resp.StatusCode, resp.Header, body)
	}
	for _, bad := range []string{"SHORT", "IIIIIIIIIIIII", "ABCD%20EFGHJKMNP", "ABCDEFGHJKMNP.json"} {
		if resp, _ := r.req(http.MethodGet, "/.well-known/oauth-protected-resource/mcp/c/"+bad, "", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", bad, resp.StatusCode)
		}
	}
}
