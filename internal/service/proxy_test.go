package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func openAIConfig() domain.ProxyConfig {
	return domain.ProxyConfig{UpstreamURL: "https://api.openai.com/v1/", Auth: domain.ProxyAuthBearer,
		Methods: []string{"post"}, Paths: []string{"/chat/completions/"}, Enabled: true}
}

func TestNormalizeProxyConfig(t *testing.T) {
	e := newEnv(t)
	good, err := e.svc.normalizeProxyConfig(openAIConfig())
	if err != nil {
		t.Fatal(err)
	}
	if good.UpstreamURL != "https://api.openai.com/v1" || strings.Join(good.AllowedHosts, ",") != "api.openai.com" ||
		strings.Join(good.Methods, ",") != "POST" || strings.Join(good.Paths, ",") != "/chat/completions" {
		t.Fatalf("normalized: %+v", good)
	}
	tg, err := e.svc.normalizeProxyConfig(domain.ProxyConfig{UpstreamURL: "https://api.telegram.org/bot{secret}",
		Auth: domain.ProxyAuthPath})
	if err != nil || tg.UpstreamURL != "https://api.telegram.org/bot{secret}" || strings.Join(tg.Methods, ",") != "GET,POST" {
		t.Fatalf("telegram: %+v %v", tg, err)
	}
	hdr, err := e.svc.normalizeProxyConfig(domain.ProxyConfig{UpstreamURL: "https://crm.example.com/api",
		Auth: domain.ProxyAuthHeader, AuthName: "X-Api-Key", AllowedHosts: []string{"CDN.example.com.", "crm.example.com"}})
	if err != nil || strings.Join(hdr.AllowedHosts, ",") != "crm.example.com,cdn.example.com" {
		t.Fatalf("header: %+v %v", hdr, err)
	}
	bad := []domain.ProxyConfig{
		{UpstreamURL: "http://api.example.com", Auth: "bearer"},
		{UpstreamURL: "https://10.0.0.5/api", Auth: "bearer"},
		{UpstreamURL: "https://localhost/api", Auth: "bearer"},
		{UpstreamURL: "https://intranet/api", Auth: "bearer"},
		{UpstreamURL: "https://site.pp.test/api", Auth: "bearer"},
		{UpstreamURL: "https://api." + testBase, Auth: "bearer"},
		{UpstreamURL: "https://u:p@api.example.com", Auth: "bearer"},
		{UpstreamURL: "https://api.example.com:8443/x", Auth: "bearer"},
		{UpstreamURL: "https://api.example.com/v1?api_key=sk-1", Auth: "bearer"},
		{UpstreamURL: "https://api.example.com/v1", Auth: "magic"},
		{UpstreamURL: "https://api.example.com/v1", Auth: "header", AuthName: "Cookie"},
		{UpstreamURL: "https://api.example.com/v1", Auth: "header"},
		{UpstreamURL: "https://api.example.com/v1", Auth: "query", AuthName: "a b"},
		{UpstreamURL: "https://api.example.com/v1", Auth: "path"},
		{UpstreamURL: "https://api.example.com/{secret}/{secret}", Auth: "path"},
		{UpstreamURL: "https://api.example.com/{secret}", Auth: "bearer"},
		{UpstreamURL: "https://api.example.com", Auth: "bearer", Methods: []string{"CONNECT"}},
		{UpstreamURL: "https://api.example.com", Auth: "bearer", Paths: []string{"x"}},
		{UpstreamURL: "https://api.example.com", Auth: "bearer", Paths: []string{"/a/../b"}},
		{UpstreamURL: "https://api.example.com", Auth: "bearer", AllowedHosts: []string{"*.example.com"}},
		{UpstreamURL: "https://api.example.com", Auth: "bearer", AllowedHosts: []string{"127.0.0.1"}},
	}
	for _, c := range bad {
		if _, err := e.svc.normalizeProxyConfig(c); domain.AsError(err).Code != domain.CodeProxyInvalid {
			t.Errorf("%+v: want proxy_invalid, got %v", c, err)
		}
	}
	for _, n := range []string{"", "-a", "A", "a_b", strings.Repeat("a", 41)} {
		if _, err := normalizeProxyName(n); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}

func TestProxyEndpointLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ann := e.user("ann@pp.test")
	bob := e.user("bob@pp.test")
	v, err := e.svc.CreateSite(ctx, ann, "Bot", "bot")
	if err != nil {
		t.Fatal(err)
	}
	mcpAnn := ann
	mcpAnn.ViaMCP = true

	view, created, err := e.svc.PutProxyEndpoint(ctx, mcpAnn, "bot", "openai", openAIConfig())
	if err != nil || !created || view.SecretSet || view.Endpoint.SecretEnc != "" ||
		view.CallURL != "https://bot."+testBase+"/_dropsite/proxy/openai/" || !strings.HasSuffix(view.SecretURL, "/sites/"+v.Site.ID+"/proxy#proxy-openai") {
		t.Fatalf("create: %+v %v %v", view, created, err)
	}
	if _, err := e.svc.SetProxySecret(ctx, mcpAnn, "bot", "openai", "sk-test-123"); domain.AsError(err).Code != domain.CodeProxySecretUIOnly {
		t.Fatalf("assistant set a secret: %v", err)
	}
	if _, err := e.svc.SetProxySecret(ctx, ann, "bot", "openai", " \n "); domain.AsError(err).Code != domain.CodeProxySecret {
		t.Fatalf("empty secret: %v", err)
	}
	view, err = e.svc.SetProxySecret(ctx, ann, "bot", "openai", "sk-test-123")
	if err != nil || !view.SecretSet || view.Endpoint.SecretEnc != "" || view.Endpoint.SecretUpdatedAt == nil {
		t.Fatalf("set secret: %+v %v", view, err)
	}

	// Rights: foreign site and foreign endpoint look missing.
	_, err = e.svc.ListProxyEndpoints(ctx, bob, "bot")
	wantNotFound(t, err)
	_, _, err = e.svc.PutProxyEndpoint(ctx, bob, v.Site.ID, "openai", openAIConfig())
	wantNotFound(t, err)
	wantNotFound(t, e.svc.DeleteProxyEndpoint(ctx, bob, "bot", "openai"))
	wantNotFound(t, e.svc.ClearProxySecret(ctx, ann, "bot", "nope"))

	// Narrowing paths keeps the key, changing the destination drops it.
	cfg := openAIConfig()
	cfg.Paths = []string{"/chat/completions", "/models"}
	view, created, err = e.svc.PutProxyEndpoint(ctx, mcpAnn, "bot", "openai", cfg)
	if err != nil || created || !view.SecretSet {
		t.Fatalf("paths update: %+v %v", view, err)
	}
	cfg.UpstreamURL = "https://evil.example.org/v1"
	view, _, err = e.svc.PutProxyEndpoint(ctx, mcpAnn, "bot", "openai", cfg)
	if err != nil || view.SecretSet {
		t.Fatalf("destination change kept the key: %+v %v", view, err)
	}

	list, err := e.svc.ListProxyEndpoints(ctx, ann, "bot")
	if err != nil || len(list) != 1 || list[0].SecretSet {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := e.svc.DeleteProxyEndpoint(ctx, mcpAnn, "bot", "openai"); err != nil {
		t.Fatal(err)
	}

	got := e.auditActions(siteTarget(v.Site.ID))
	want := []string{"proxy.endpoint.create", "proxy.secret.set", "proxy.endpoint.update", "proxy.endpoint.update", "proxy.endpoint.delete"}
	if strings.Join(got[len(got)-len(want):], ",") != strings.Join(want, ",") {
		t.Fatalf("audit: %v", got)
	}
	rows, _, err := e.svc.ListAudit(ctx, e.admin, AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	cleared := false
	for _, r := range rows {
		if strings.Contains(fmt.Sprint(r.Meta), "sk-test-123") {
			t.Fatalf("secret in audit: %+v", r)
		}
		if r.Action == "proxy.endpoint.update" && r.Meta["secret_cleared"] == true {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("audit lacks secret_cleared")
	}
}

func TestProxyEndpointLimitAndPurge(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ann := e.user("ann@pp.test")
	v, err := e.svc.CreateSite(ctx, ann, "Bot", "bot")
	if err != nil {
		t.Fatal(err)
	}
	for i := range ProxyEndpointsPerSite {
		if _, _, err := e.svc.PutProxyEndpoint(ctx, ann, "bot", fmt.Sprintf("e%d", i), openAIConfig()); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = e.svc.PutProxyEndpoint(ctx, ann, "bot", "one-more", openAIConfig())
	wantCode(t, err, domain.CodeProxyLimit)
	if _, _, err := e.svc.PutProxyEndpoint(ctx, ann, "bot", "e0", openAIConfig()); err != nil {
		t.Fatalf("replacing at the limit: %v", err)
	}
	if err := e.svc.DeleteSite(ctx, ann, "bot"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Housekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := e.repo.CountProxyEndpoints(ctx, v.Site.ID); err != nil || n != 0 {
		t.Fatalf("endpoints left after purge: %d %v", n, err)
	}
}
