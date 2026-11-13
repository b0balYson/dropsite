package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/store"
)

func chatTemplate() *domain.ProxyTemplate {
	return &domain.ProxyTemplate{
		Path: "/chat/completions",
		Body: json.RawMessage(`{"model":"m-1","max_tokens":200,"messages":[{"role":"system","content":"Be brief."},` +
			`{"role":"user","content":"{{question}}"}],"user":"site {{lang}}"}`),
		Fields: map[string]domain.ProxyField{
			"question": {Type: "string", MaxLength: 50, Required: true},
			"lang":     {Type: "string", MaxLength: 5},
		},
		Response: "choices.0.message.content",
	}
}

func TestNormalizeProxyTemplate(t *testing.T) {
	good, err := normalizeProxyTemplate(*chatTemplate())
	if err != nil || good.Method != "POST" || good.Fields["lang"].MaxLength != 5 {
		t.Fatalf("good: %+v %v", good, err)
	}
	msgs := domain.ProxyTemplate{Body: json.RawMessage(`{"messages":"{{history}}"}`),
		Fields: map[string]domain.ProxyField{"history": {Type: "messages"}}}
	if m, err := normalizeProxyTemplate(msgs); err != nil || m.Fields["history"].MaxItems != proxyDefaultMsgItems {
		t.Fatalf("messages: %+v %v", m, err)
	}
	bad := map[string]domain.ProxyTemplate{
		"no fields":         {Body: json.RawMessage(`{"a":1}`)},
		"unknown field":     {Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"y": {Type: "string"}}},
		"unused field":      {Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"x": {Type: "string"}, "y": {Type: "string"}}},
		"bad type":          {Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"x": {Type: "code"}}},
		"bad name":          {Body: json.RawMessage(`{"a":"{{X}}"}`), Fields: map[string]domain.ProxyField{"X": {Type: "string"}}},
		"not json":          {Body: json.RawMessage(`{"a":`), Fields: map[string]domain.ProxyField{"x": {Type: "string"}}},
		"messages in text":  {Body: json.RawMessage(`{"a":"hi {{h}}"}`), Fields: map[string]domain.ProxyField{"h": {Type: "messages"}}},
		"method GET":        {Method: "GET", Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"x": {Type: "string"}}},
		"path with marks":   {Path: "/a?b", Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"x": {Type: "string"}}},
		"response not path": {Response: "a[0]", Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"x": {Type: "string"}}},
		"too long":          {Body: json.RawMessage(`{"a":"{{x}}","b":"` + strings.Repeat("x", 17000) + `"}`), Fields: map[string]domain.ProxyField{"x": {Type: "string"}}},
		"huge string":       {Body: json.RawMessage(`{"a":"{{x}}"}`), Fields: map[string]domain.ProxyField{"x": {Type: "string", MaxLength: 50000}}},
	}
	for name, tm := range bad {
		if _, err := normalizeProxyTemplate(tm); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProxyClassConfig(t *testing.T) {
	e := newEnv(t)
	base := domain.ProxyConfig{UpstreamURL: "https://api.openai.com/v1", Auth: domain.ProxyAuthBearer, Enabled: true}
	c := base
	c.Class = "ai"
	if _, err := e.svc.normalizeProxyConfig(c); err == nil {
		t.Fatal("ai without a template accepted")
	}
	c.Template = chatTemplate()
	out, err := e.svc.normalizeProxyConfig(c)
	if err != nil || out.Methods[0] != "POST" || out.Paths[0] != "/chat/completions" {
		t.Fatalf("ai: %+v %v", out, err)
	}
	r := base
	r.Class = "read"
	if out, err := e.svc.normalizeProxyConfig(r); err != nil || strings.Join(out.Methods, ",") != "GET,HEAD" {
		t.Fatalf("read: %+v %v", out, err)
	}
	r.Methods = []string{"POST"}
	if _, err := e.svc.normalizeProxyConfig(r); err == nil {
		t.Fatal("read with POST accepted")
	}
	tr := base
	tr.Class, tr.Template = "transparent", chatTemplate()
	if _, err := e.svc.normalizeProxyConfig(tr); err == nil {
		t.Fatal("template on transparent accepted")
	}
	tr.Template, tr.Limits = nil, domain.ProxyLimits{PerMinute: 10000}
	if _, err := e.svc.normalizeProxyConfig(tr); err == nil {
		t.Fatal("per_minute out of range accepted")
	}
	if l := ProxyEffectiveLimits(domain.ProxyConfig{Class: "ai", Limits: domain.ProxyLimits{DailyBudget: 7}}); l.PerMinute != 20 ||
		l.DailyBudget != 7 || l.SessionQuota != 50 {
		t.Fatalf("effective: %+v", l)
	}
}

func TestNewEndpointDefaultsToAI(t *testing.T) {
	r := newProxyRig(t)
	ctx := context.Background()
	cfg := domain.ProxyConfig{UpstreamURL: "https://api.example.com", Auth: domain.ProxyAuthBearer, Enabled: true}
	_, _, err := r.svc.PutProxyEndpoint(ctx, r.owner, "bot", "x", cfg)
	if de := domain.AsError(err); de.Code != domain.CodeProxyInvalid || !strings.Contains(de.Message, "template") {
		t.Fatalf("new endpoint without class: %v", err)
	}
	cfg.Template = chatTemplate()
	v, created, err := r.svc.PutProxyEndpoint(ctx, r.owner, "bot", "x", cfg)
	if err != nil || !created || v.Endpoint.Class != domain.ProxyClassAI {
		t.Fatalf("default class: %+v %v", v.Endpoint, err)
	}
	cfg.Template = nil
	v, _, err = r.svc.PutProxyEndpoint(ctx, r.owner, "bot", "x", cfg)
	if err != nil || v.Endpoint.Class != domain.ProxyClassAI || v.Endpoint.Template == nil {
		t.Fatalf("update keeps class and template: %+v %v", v.Endpoint, err)
	}
}

func TestRenderAndExtract(t *testing.T) {
	tm, _ := normalizeProxyTemplate(*chatTemplate())
	vals, err := parseProxyFields(tm, []byte(`{"question":"Привет \"мир\"","lang":"ru"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := renderProxyBody(tm, vals)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	msgs := got["messages"].([]any)
	if msgs[1].(map[string]any)["content"] != `Привет "мир"` || got["user"] != "site ru" || got["max_tokens"] != float64(200) {
		t.Fatalf("rendered: %s", body)
	}
	for name, in := range map[string]string{
		"unknown": `{"question":"a","model":"gpt-9"}`, "missing": `{"lang":"en"}`, "too long": `{"question":"` + strings.Repeat("a", 51) + `"}`,
		"type": `{"question":5}`, "not object": `[1]`,
	} {
		if _, err := parseProxyFields(tm, []byte(in)); domain.AsError(err).Code != domain.CodeProxyField {
			t.Errorf("%s: %v", name, err)
		}
	}
	mt := domain.ProxyTemplate{Fields: map[string]domain.ProxyField{"h": {Type: "messages", MaxItems: 2, MaxLength: 10},
		"n": {Type: "integer"}, "ok": {Type: "boolean"}}}
	if _, err := parseProxyFields(mt, []byte(`{"h":[{"role":"system","content":"x"}]}`)); err == nil {
		t.Fatal("system role accepted")
	}
	if _, err := parseProxyFields(mt, []byte(`{"h":[{"role":"user","content":"x"}],"n":1.5}`)); err == nil {
		t.Fatal("fraction as integer accepted")
	}
	if v, err := parseProxyFields(mt, []byte(`{"h":[{"role":"user","content":"x"}],"n":3,"ok":true}`)); err != nil || v["ok"] != true {
		t.Fatalf("valid: %v %v", v, err)
	}
	var doc any
	_ = json.Unmarshal([]byte(`{"choices":[{"message":{"content":"hi"}}]}`), &doc)
	if v, ok := extractJSONPath(doc, "choices.0.message.content"); !ok || v != "hi" {
		t.Fatalf("extract: %v %v", v, ok)
	}
	for _, p := range []string{"choices.1.message", "choices.x", "nope", "choices.01.message"} {
		if _, ok := extractJSONPath(doc, p); ok {
			t.Errorf("extract %s found", p)
		}
	}
	if out, ok := reduceSSELine(`data: {"choices":[{"delta":{"content":"He"}}]}`, "choices.0.delta.content"); !ok || out != `"He"` {
		t.Fatalf("sse: %q %v", out, ok)
	}
	if _, ok := reduceSSELine(`event: ping`, "a"); ok {
		t.Fatal("non-data line kept")
	}
}

// solvePow solves a session or send challenge like session.js.
func solvePow(t *testing.T, svc *Service, host, ip, purpose string) string {
	t.Helper()
	ch, err := svc.SessionChallenge(context.Background(), host, ip, purpose)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Parameters.Algorithm != "SHA-256" {
		t.Fatalf("algorithm %q", ch.Parameters.Algorithm)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeySHA(), Timeout: time.Minute})
	if err != nil || sol == nil {
		t.Fatalf("solve: %v", err)
	}
	b, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	return base64.StdEncoding.EncodeToString(b)
}

func (r *proxyRig) session(ip string) string {
	r.t.Helper()
	g, err := r.svc.IssueSession(context.Background(), r.host, ip, solvePow(r.t, r.svc, r.host, ip, forms.PurposeSession))
	if err != nil {
		r.t.Fatal(err)
	}
	return g.Token
}

func (r *proxyRig) aiCall(ip, token, body string, extra ...string) (*ProxyResponse, string, error) {
	h := http.Header{}
	if token != "" {
		h.Set(SessionHeader, token)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		h.Set(extra[i], extra[i+1])
	}
	return r.call(ProxyCall{Name: "chat", IP: ip, Header: h, Body: strings.NewReader(body)})
}

func chatUpstream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"x","usage":{"total_tokens":9},"choices":[{"message":{"content":"Hello!"}}]}`)
}

func TestSessionAndAIEndpoint(t *testing.T) {
	r := newProxyRig(t)
	r.up.handler = chatUpstream
	r.endpoint("chat", domain.ProxyConfig{Class: domain.ProxyClassAI, UpstreamURL: "https://api.example.com/v1",
		Auth: domain.ProxyAuthBearer, Enabled: true, Template: chatTemplate(),
		Limits: domain.ProxyLimits{SessionQuota: 2}}, "sk-1")
	ip := "203.0.113.7"
	if _, _, err := r.aiCall(ip, "", `{"question":"hi"}`); domain.AsError(err).Code != domain.CodeProxySession {
		t.Fatalf("without session: %v", err)
	}
	tok := r.session(ip)
	resp, body, err := r.aiCall(ip, tok, `{"question":"hi"}`)
	if err != nil || resp.Status != http.StatusOK || body != `{"result":"Hello!"}` {
		t.Fatalf("call: %v %q", err, body)
	}
	seen := r.up.last(t)
	if seen.URI != "/v1/chat/completions" || seen.Header.Get("Authorization") != "Bearer sk-1" ||
		!strings.Contains(seen.Body, `"content":"hi"`) || seen.Header.Get(SessionHeader) != "" {
		t.Fatalf("upstream got %+v", seen)
	}
	if _, _, err := r.aiCall("198.51.100.1", tok, `{"question":"hi"}`); domain.AsError(err).Code != domain.CodeProxySession {
		t.Fatalf("token from another IP: %v", err)
	}
	if _, _, err := r.aiCall(ip, tok, `{"question":"hi","model":"big"}`); domain.AsError(err).Code != domain.CodeProxyField {
		t.Fatalf("extra field: %v", err)
	}
	if _, _, err := r.aiCall(ip, tok, `{"question":"hi"}`); domain.AsError(err).Code != domain.CodeProxySession {
		t.Fatalf("quota of 2 not enforced: %v", err)
	}
	if _, _, err := r.aiCall(ip, r.session(ip), `{"question":"again"}`); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	r.clock.Advance(ProxySessionTTL + time.Second)
	if _, _, err := r.aiCall(ip, tok, `{"question":"hi"}`); domain.AsError(err).Code != domain.CodeProxySession {
		t.Fatalf("expired token: %v", err)
	}
	// A solution is spent once.
	pay := solvePow(t, r.svc, r.host, ip, forms.PurposeSession)
	if _, err := r.svc.IssueSession(context.Background(), r.host, ip, pay); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.IssueSession(context.Background(), r.host, ip, pay); err == nil {
		t.Fatal("replayed solution accepted")
	}
	// A session of another site is useless here.
	if _, err := r.svc.CreateSite(context.Background(), r.owner, "Other", "other"); err != nil {
		t.Fatal(err)
	}
	other := "other." + testBase
	g, err := r.svc.IssueSession(context.Background(), other, ip, solvePow(t, r.svc, other, ip, forms.PurposeSession))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.aiCall(ip, g.Token, `{"question":"hi"}`); domain.AsError(err).Code != domain.CodeProxySession {
		t.Fatalf("token of another site: %v", err)
	}
}

func TestSessionBoundToIPv6Net(t *testing.T) {
	r := newProxyRig(t)
	r.up.handler = chatUpstream
	r.endpoint("chat", domain.ProxyConfig{Class: domain.ProxyClassAI, UpstreamURL: "https://api.example.com/v1",
		Auth: domain.ProxyAuthBearer, Enabled: true, Template: chatTemplate()}, "sk-1")
	tok := r.session("2001:db8:1:2::10")
	if _, _, err := r.aiCall("2001:db8:1:2::99", tok, `{"question":"hi"}`); err != nil {
		t.Fatalf("same /64: %v", err)
	}
	if _, _, err := r.aiCall("2001:db8:1:3::10", tok, `{"question":"hi"}`); domain.AsError(err).Code != domain.CodeProxySession {
		t.Fatalf("other /64: %v", err)
	}
}

func TestSendEndpointNeedsFreshPow(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("chat", domain.ProxyConfig{Class: domain.ProxyClassSend, UpstreamURL: "https://api.telegram.example.com/bot{secret}",
		Auth: domain.ProxyAuthPath, Enabled: true, Template: &domain.ProxyTemplate{Path: "/sendMessage",
			Body:   json.RawMessage(`{"chat_id":12345,"text":"From the site: {{text}}"}`),
			Fields: map[string]domain.ProxyField{"text": {Type: "string", MaxLength: 200, Required: true}}}}, "123:abc")
	ip := "203.0.113.7"
	tok := r.session(ip)
	if _, _, err := r.aiCall(ip, tok, `{"text":"hi"}`); domain.AsError(err).Code != domain.CodeProxyPow {
		t.Fatalf("without pow: %v", err)
	}
	pow := solvePow(t, r.svc, r.host, ip, forms.PurposeSend)
	if _, _, err := r.aiCall(ip, tok, `{"text":"hi"}`, PowHeader, pow); err != nil {
		t.Fatalf("with pow: %v", err)
	}
	seen := r.up.last(t)
	if seen.URI != "/bot123:abc/sendMessage" || seen.Body != `{"chat_id":12345,"text":"From the site: hi"}` {
		t.Fatalf("upstream got %+v", seen)
	}
	if _, _, err := r.aiCall(ip, tok, `{"text":"hi"}`, PowHeader, pow); domain.AsError(err).Code != domain.CodeProxyPow {
		t.Fatalf("replayed pow: %v", err)
	}
	// A session solution is not a send proof.
	if _, _, err := r.aiCall(ip, tok, `{"text":"hi"}`, PowHeader, solvePow(t, r.svc, r.host, ip, forms.PurposeSession)); domain.AsError(err).Code != domain.CodeProxyPow {
		t.Fatalf("session solution as pow: %v", err)
	}
}

func TestDailyBudgetPausesEndpoint(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("data", domain.ProxyConfig{Class: domain.ProxyClassRead, UpstreamURL: "https://api.example.com",
		Auth: domain.ProxyAuthBearer, Enabled: true, Limits: domain.ProxyLimits{DailyBudget: 2}}, "sk")
	get := func() error {
		_, _, err := r.call(ProxyCall{Name: "data", Method: http.MethodGet, Path: "/x"})
		return err
	}
	if _, _, err := r.call(ProxyCall{Name: "data", Method: http.MethodPost}); domain.AsError(err).Code != domain.CodeProxyMethod {
		t.Fatalf("POST on read: %v", err)
	}
	for i := range 2 {
		if err := get(); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	for range 3 {
		err := get()
		if de := domain.AsError(err); de.Code != domain.CodeProxyBudget || de.RetryAfter <= 0 {
			t.Fatalf("over budget: %v", err)
		}
	}
	if r.up.count() != 2 {
		t.Fatalf("upstream calls = %d", r.up.count())
	}
	v, err := r.svc.GetProxyEndpoint(context.Background(), r.owner, "bot", "data")
	if err != nil || !v.Endpoint.Usage.Paused || v.Endpoint.Usage.Today != 2 {
		t.Fatalf("view: %+v %v", v.Endpoint.Usage, err)
	}
	rows, _, _ := r.repo.ListAudit(context.Background(), store.AuditFilter{Action: "proxy.budget.exhausted", Page: store.Page{Limit: 10}})
	if len(rows) != 1 {
		t.Fatalf("budget audit entries = %d", len(rows))
	}
	r.clock.Advance(24 * time.Hour)
	if err := get(); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestBansLadderAndMarkers(t *testing.T) {
	r := newProxyRig(t)
	ctx := context.Background()
	r.endpoint("ai", domain.ProxyConfig{Class: domain.ProxyClassTransparent, UpstreamURL: "https://api.example.com",
		Auth: domain.ProxyAuthBearer, Enabled: true}, "sk")
	b, err := r.svc.AddBanCLI(ctx, "203.0.113.7", 0)
	if err != nil || b.Level != 1 || b.ExpiresAt.Sub(r.clock.Now()) != 15*time.Minute {
		t.Fatalf("ban: %+v %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(r.root, "bans", "203.0.113.7")); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if _, _, err := r.call(ProxyCall{Name: "ai"}); domain.AsError(err).Kind != domain.KindRateLimited {
		t.Fatalf("banned call: %v", err)
	}
	if _, err := r.svc.ActiveBans(ctx, r.owner); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("owner lists bans: %v", err)
	}
	r.clock.Advance(16 * time.Minute)
	if err := r.svc.expireBans(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.root, "bans", "203.0.113.7")); !os.IsNotExist(err) {
		t.Fatalf("expired marker kept: %v", err)
	}
	if _, _, err := r.call(ProxyCall{Name: "ai"}); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if b, _ := r.svc.AddBanCLI(ctx, "203.0.113.7", 0); b.Level != 2 || b.ExpiresAt.Sub(r.clock.Now()) != time.Hour {
		t.Fatalf("repeat ban: %+v", b)
	}
	if err := r.svc.ClearBan(ctx, r.admin, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.svc.ActiveBans(ctx, r.admin); len(list) != 0 {
		t.Fatalf("after clear: %v", list)
	}
	if _, err := os.Stat(filepath.Join(r.root, "bans", "203.0.113.7")); !os.IsNotExist(err) {
		t.Fatalf("cleared marker kept: %v", err)
	}
	// IPv6: the whole /64 is refused by control; Caddy markers follow the
	// addresses seen in the access log.
	if _, err := r.svc.AddBanCLI(ctx, "2001:db8:1:2::5", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.call(ProxyCall{Name: "ai", IP: "2001:db8:1:2::77"}); domain.AsError(err).Kind != domain.KindRateLimited {
		t.Fatalf("other address of a banned /64: %v", err)
	}
	r.svc.processAccess(ctx, []byte(`{"ts":`+jsonTS(r.clock.Now())+`,"request":{"client_ip":"2001:db8:1:2::77"},"status":200}`))
	if _, err := os.Stat(filepath.Join(r.root, "bans", "2001:db8:1:2::77")); err != nil {
		t.Fatalf("marker for a seen address: %v", err)
	}
	if err := r.svc.ClearBan(ctx, r.admin, "2001:db8:1:2::/64"); err != nil {
		t.Fatal(err)
	}
	if left, _ := r.svc.markers.Banned(); len(left) != 0 {
		t.Fatalf("markers left: %v", left)
	}
}

func jsonTS(t time.Time) string {
	b, _ := json.Marshal(float64(t.UnixMilli()) / 1000)
	return string(b)
}

func TestRateViolationsInLogBan(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	logPath := filepath.Join(t.TempDir(), "sites-access.log")
	e.svc.cfg.StatsLogFile = logPath
	if err := os.WriteFile(logPath, []byte("{\"ts\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var lg guardLog
	if err := e.svc.scanAccessLog(ctx, &lg); err != nil || !lg.started {
		t.Fatalf("start: %v", err)
	}
	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	defer f.Close()
	for i := range 3 {
		line := `{"ts":` + jsonTS(e.clock.Now()) + `,"request":{"client_ip":"198.51.100.9"},"status":429,` +
			`"resp_headers":{"X-Dropsite-Block":["rate"]}}` + "\n"
		for range 50 {
			_, _ = f.WriteString(line)
		}
		if err := e.svc.scanAccessLog(ctx, &lg); err != nil {
			t.Fatal(err)
		}
		banned, _ := e.svc.netBanned(ctx, "198.51.100.9")
		if banned != (i == 2) {
			t.Fatalf("after violation %d: banned=%v", i+1, banned)
		}
		e.clock.Advance(2 * time.Minute)
	}
	if _, err := os.Stat(filepath.Join(e.root, "bans", "198.51.100.9")); err != nil {
		t.Fatalf("marker: %v", err)
	}
}

func TestPeriodicCallsEscalateToBan(t *testing.T) {
	r := newProxyRig(t)
	r.up.handler = chatUpstream
	r.endpoint("chat", domain.ProxyConfig{Class: domain.ProxyClassAI, UpstreamURL: "https://api.example.com/v1",
		Auth: domain.ProxyAuthBearer, Enabled: true, Template: chatTemplate(),
		Limits: domain.ProxyLimits{SessionQuota: 1000, DailyBudget: 1000}}, "sk-1")
	ip := "203.0.113.50"
	tok := r.session(ip)
	for i := range 10 {
		if _, _, err := r.aiCall(ip, tok, `{"question":"tick"}`); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		r.clock.Advance(20 * time.Second)
	}
	if l := r.svc.suspicionLevel(ip); l != 1 {
		t.Fatalf("level after a periodic run = %d", l)
	}
	if _, err := r.svc.IssueSession(context.Background(), r.host, ip, "x"); err == nil {
		t.Fatal("garbage accepted")
	}
	// A flagged network still gets challenges (ten times harder) and a capped quota.
	ch, err := r.svc.SessionChallenge(context.Background(), r.host, ip, forms.PurposeSession)
	if err != nil || ch.Parameters.Cost != 1 {
		t.Fatal(err)
	}
	g, err := r.svc.IssueSession(context.Background(), r.host, ip, solvePow(t, r.svc, r.host, ip, forms.PurposeSession))
	if err != nil || g.Quota != 10 {
		t.Fatalf("quota: %+v %v", g, err)
	}
	// The script keeps going with new sessions whenever the quota runs out.
	for range 2 {
		for i := range 10 {
			if i%5 == 0 {
				tok = r.session(ip)
			}
			_, _, err = r.aiCall(ip, tok, `{"question":"tick"}`)
			r.clock.Advance(20 * time.Second)
		}
	}
	if banned, _ := r.svc.netBanned(context.Background(), ip); !banned {
		t.Fatalf("not banned after three periodic runs (last error %v, level %d)", err, r.svc.suspicionLevel(ip))
	}
}

func TestDefenderSwitches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	off := filepath.Join(e.root, "flags", "defender-off")
	strict := filepath.Join(e.root, "flags", "defender-strict")
	if st, err := e.svc.GetDefender(ctx, e.admin); err != nil || !st.Enabled || st.Strict {
		t.Fatalf("default: %+v %v", st, err)
	}
	if err := e.svc.SetDefender(ctx, e.admin, DefenderStatus{Enabled: false, Strict: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(off); err != nil {
		t.Fatalf("off marker: %v", err)
	}
	if _, err := os.Stat(strict); err != nil {
		t.Fatalf("strict marker: %v", err)
	}
	if err := e.svc.SetDefenderCLI(ctx, DefenderStatus{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(off); !os.IsNotExist(err) {
		t.Fatal("off marker kept")
	}
	user := e.user("u@pp.test")
	if err := e.svc.SetDefender(ctx, user, DefenderStatus{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-admin: %v", err)
	}
	mcp := e.admin
	mcp.ViaMCP = true
	if _, err := e.svc.GetDefender(ctx, mcp); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("via MCP: %v", err)
	}
	// Startup brings the markers back in line with the database.
	_ = os.WriteFile(off, nil, 0o644)
	if err := e.svc.reconcileGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(off); !os.IsNotExist(err) {
		t.Fatal("reconcile kept a stale marker")
	}
}
