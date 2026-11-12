package mcp

import (
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// No proxy tool may accept a key: keys are pasted in the web interface.
func TestProxyToolsTakeNoSecret(t *testing.T) {
	for _, build := range []func() (*jsonschema.Schema, error){
		func() (*jsonschema.Schema, error) { return jsonschema.For[proxySetArgs](nil) },
		func() (*jsonschema.Schema, error) { return jsonschema.For[proxyDeleteArgs](nil) },
	} {
		schema, err := build()
		if err != nil {
			t.Fatal(err)
		}
		for name := range schema.Properties {
			if n := strings.ToLower(name); strings.Contains(n, "secret") || strings.Contains(n, "key") || strings.Contains(n, "token") {
				t.Errorf("tool input has a %q property", name)
			}
		}
	}
}

func TestProxySummaryAndNextStep(t *testing.T) {
	e := ProxyEndpoint{Name: "openai", UpstreamURL: "https://api.openai.com/v1", Methods: []string{"POST"},
		Paths: []string{"/chat/completions"}, Enabled: true, SecretURL: "https://ui.example/sites/s1/proxy#proxy-openai",
		Usage: ProxyUsage{Requests: 3, Errors: 1, Rejected: 2, LastUsedAt: "2026-11-12T10:00:00Z", LastStatus: 200}}
	s := ProxySummary(e)
	for _, want := range []string{"POST", "only /chat/completions", "not set", e.SecretURL, "3 calls forwarded, 1 failed, 2 refused", "status 200"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q: %s", want, s)
		}
	}
	next := ProxyNextStep(e)
	if !strings.Contains(next, e.SecretURL) || !strings.Contains(next, "/_dropsite/proxy/openai/") || !strings.Contains(next, "Do not ask") {
		t.Errorf("next step: %s", next)
	}
	e.SecretSet, e.Enabled = true, false
	if s := ProxySummary(e); !strings.Contains(s, "turned off") {
		t.Errorf("off: %s", s)
	}
	e.Enabled = true
	if next := ProxyNextStep(e); strings.Contains(next, e.SecretURL) {
		t.Errorf("next step with a key: %s", next)
	}
}
