package main

import (
	"context"
	"strings"
	"testing"
)

func TestStdioProxyTools(t *testing.T) {
	r := newParityRig(t)
	ctx := context.Background()
	ann := r.env.User("ann@pp.test")
	tok := r.env.Token(ann, "cli")
	local, web := r.stdio(tok), r.web(tok)
	v, err := r.env.Svc.CreateSite(ctx, ann, "Bot", "bot")
	if err != nil {
		t.Fatal(err)
	}

	args := map[string]any{"site": "bot", "name": "openai", "upstream_url": "https://api.openai.com/v1",
		"auth": "bearer", "methods": []string{"POST"}, "paths": []string{"/chat/completions"}}
	out := mustTool(t, local, "set_proxy_endpoint", args)
	ep := out["endpoint"].(map[string]any)
	if out["created"] != true || ep["secret_set"] != false || !strings.Contains(ep["secret_url"].(string), "/sites/"+v.Site.ID+"/proxy") ||
		!strings.Contains(out["next_step"].(string), ep["secret_url"].(string)) || !strings.Contains(ep["summary"].(string), "not set") {
		t.Fatalf("set_proxy_endpoint: %v", out)
	}
	again := same(t, local, web, "set_proxy_endpoint", args)
	if again["created"] != false {
		t.Fatalf("replace: %v", again)
	}
	same(t, local, web, "set_proxy_endpoint", map[string]any{"site": "bot", "name": "bad", "upstream_url": "https://10.0.0.1",
		"auth": "bearer"})
	same(t, local, web, "set_proxy_endpoint", map[string]any{"site": "bot", "name": "", "upstream_url": "https://a.example.com",
		"auth": "bearer"})
	wantToolErr(t, local, "set_proxy_endpoint", map[string]any{"site": "bot", "name": "k",
		"upstream_url": "https://api.example.com?api_key=sk-1", "auth": "bearer"}, "web interface")

	if _, err := r.env.Svc.SetProxySecret(ctx, ann, "bot", "openai", "sk-hidden-1"); err != nil {
		t.Fatal(err)
	}
	list := same(t, local, web, "list_proxy_endpoints", map[string]any{"site": "bot"})
	eps := list["endpoints"].([]any)
	if len(eps) != 1 || eps[0].(map[string]any)["secret_set"] != true || strings.Contains(stringify(list), "sk-hidden-1") {
		t.Fatalf("list: %v", list)
	}

	same(t, local, web, "delete_proxy_endpoint", map[string]any{"site": "bot", "name": "openai", "confirm": "x"})
	same(t, local, web, "delete_proxy_endpoint", map[string]any{"site": "bot", "name": "nope", "confirm": "nope"})
	wantToolErr(t, local, "delete_proxy_endpoint", map[string]any{"site": "bot", "name": "nope", "confirm": "nope"}, "list_proxy_endpoints")
	mustTool(t, local, "delete_proxy_endpoint", map[string]any{"site": "bot", "name": "openai", "confirm": "openai"})
	same(t, local, web, "list_proxy_endpoints", map[string]any{"site": "bot"})

	// Another user's site does not exist for this user.
	bob := r.env.User("bob@pp.test")
	bv, err := r.env.Svc.CreateSite(ctx, bob, "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]map[string]any{
		"list_proxy_endpoints":  {"site": bv.Site.ID},
		"set_proxy_endpoint":    {"site": bv.Site.ID, "name": "x", "upstream_url": "https://a.example.com", "auth": "bearer"},
		"delete_proxy_endpoint": {"site": bv.Site.ID, "name": "x", "confirm": "x"},
	} {
		same(t, local, web, name, a)
		wantToolErr(t, local, name, a, "not found")
	}
}

func stringify(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			for k, val := range x {
				b.WriteString(k + "=")
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		case string:
			b.WriteString(x + ";")
		}
	}
	walk(v)
	return b.String()
}
