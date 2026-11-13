package main

import (
	"context"
	"strings"
	"testing"
)

func TestStdioProxyClasses(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	if _, err := r.env.Svc.CreateSite(ctx, ann, "Bot", "bot"); err != nil {
		t.Fatal(err)
	}
	// Without a class a new endpoint is ai, which needs a template.
	noTemplate := map[string]any{"site": "bot", "name": "chat", "upstream_url": "https://api.openai.com/v1", "auth": "bearer"}
	same(t, local, web, "set_proxy_endpoint", noTemplate)
	wantToolErr(t, local, "set_proxy_endpoint", noTemplate, "template")

	args := map[string]any{"site": "bot", "name": "chat", "upstream_url": "https://api.openai.com/v1", "auth": "bearer",
		"daily_budget": 300,
		"template": map[string]any{"path": "/chat/completions",
			"body": map[string]any{"model": "gpt-4o-mini", "max_tokens": 300,
				"messages": []any{map[string]any{"role": "system", "content": "Be brief."}, map[string]any{"role": "user", "content": "{{question}}"}}},
			"fields":   map[string]any{"question": map[string]any{"type": "string", "max_length": 500, "required": true}},
			"response": "choices.0.message.content"}}
	out := mustTool(t, local, "set_proxy_endpoint", args)
	ep := out["endpoint"].(map[string]any)
	limits := ep["effective_limits"].(map[string]any)
	if ep["class"] != "ai" || ep["warning"] != nil || limits["daily_budget"] != float64(300) || limits["session_quota"] != float64(50) ||
		!strings.Contains(out["next_step"].(string), "dropsite.fetch") || !strings.Contains(out["next_step"].(string), "question") {
		t.Fatalf("ai endpoint: %v", out)
	}
	same(t, local, web, "set_proxy_endpoint", args)

	raw := map[string]any{"site": "bot", "name": "raw", "upstream_url": "https://api.openai.com/v1", "auth": "bearer",
		"class": "transparent"}
	mustTool(t, local, "set_proxy_endpoint", raw)
	same(t, local, web, "set_proxy_endpoint", raw)
	list := same(t, local, web, "list_proxy_endpoints", map[string]any{"site": "bot"})
	var warned bool
	for _, e := range list["endpoints"].([]any) {
		m := e.(map[string]any)
		if m["name"] == "raw" {
			warned = strings.Contains(m["warning"].(string), "password-protected") &&
				strings.Contains(m["summary"].(string), "Warning")
		}
	}
	if !warned {
		t.Fatalf("transparent endpoint without a warning: %v", list)
	}
	same(t, local, web, "set_proxy_endpoint", map[string]any{"site": "bot", "name": "bad", "upstream_url": "https://api.openai.com/v1",
		"auth": "bearer", "class": "read", "methods": []string{"POST"}})
}
