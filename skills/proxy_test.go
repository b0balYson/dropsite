package skills

import (
	"strings"
	"testing"
)

func TestProxyTopic(t *testing.T) {
	text, ok := Guide("proxy")
	if !ok {
		t.Fatal("no proxy topic")
	}
	for _, want := range []string{"/_dropsite/proxy/", "secret_url", "Never ask for the key in the chat", "OpenAI",
		"CRM", "Telegram", "password-protected", "set_proxy_endpoint"} {
		if !strings.Contains(text, want) {
			t.Errorf("proxy topic lacks %q", want)
		}
	}
	if !strings.Contains(Instructions, "set_proxy_endpoint") {
		t.Error("instructions do not mention the proxy")
	}
}
