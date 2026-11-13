package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestSessionFlowAndTemplatedEndpoint(t *testing.T) {
	tr, auth := proxyUpstream(t)
	h := newHarness(t, testenv.WithProxyTransport(tr))
	ann, owner := h.user("ann@pp.test")
	site := h.createSite(owner, "Bot", "")
	base := "/api/v1/sites/" + site.ID + "/proxy/endpoints"
	host := site.ID + "." + testenv.BaseDomain
	ip := "198.51.100.30:4000"
	visit := func(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host, req.RemoteAddr = host, ip
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, req)
		return rec
	}

	rec := visit(http.MethodGet, "/_dropsite/session.js", "")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Header().Get("Content-Type"), "javascript") || rec.Body.Len() > 4096 ||
		!strings.Contains(rec.Body.String(), "X-Dropsite-Session") {
		t.Fatalf("session.js: %d bytes %v", rec.Body.Len(), rec.Header())
	}

	// A new endpoint without a class is ai and needs a template.
	rec = h.do(http.MethodPut, base+"/chat", owner, map[string]any{"upstream_url": "https://api.example.com/v1", "auth": "bearer"})
	wantError(t, rec, http.StatusBadRequest, "proxy_invalid")
	cfg := map[string]any{"upstream_url": "https://api.example.com/v1", "auth": "bearer",
		"template": map[string]any{"path": "/chat", "body": map[string]any{"model": "m", "q": "{{q}}"},
			"fields": map[string]any{"q": map[string]any{"type": "string", "max_length": 100}}},
		"limits": map[string]any{"daily_budget": 5}}
	rec = h.do(http.MethodPut, base+"/chat", owner, cfg)
	wantStatus(t, rec, http.StatusCreated)
	var ep struct {
		Class     string  `json:"class"`
		Warning   *string `json:"warning"`
		Effective struct {
			PerMinute   int `json:"per_minute"`
			DailyBudget int `json:"daily_budget"`
		} `json:"effective_limits"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ep)
	if ep.Class != "ai" || ep.Warning != nil || ep.Effective.PerMinute != 20 || ep.Effective.DailyBudget != 5 {
		t.Fatalf("created: %s", rec.Body)
	}
	if _, err := h.env.Svc.SetProxySecret(context.Background(), ann, site.ID, "chat", "sk-9"); err != nil {
		t.Fatal(err)
	}

	wantError(t, visit(http.MethodPost, "/_dropsite/proxy/chat", `{"q":"hi"}`), http.StatusUnauthorized, "proxy_session")

	rec = visit(http.MethodGet, "/_dropsite/session/challenge", "")
	wantStatus(t, rec, http.StatusOK)
	var ch altcha.Challenge
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{Challenge: ch, DeriveKey: altcha.DeriveKeySHA(), Timeout: time.Minute})
	if err != nil || sol == nil {
		t.Fatalf("solve: %v", err)
	}
	pay, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	body, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(pay)})
	rec = visit(http.MethodPost, "/_dropsite/session", string(body))
	wantStatus(t, rec, http.StatusOK)
	var g struct {
		Token string `json:"token"`
		Quota int    `json:"quota"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil || g.Token == "" || g.Quota != 50 {
		t.Fatalf("grant: %s", rec.Body)
	}
	wantError(t, visit(http.MethodPost, "/_dropsite/session", string(body)), http.StatusUnauthorized, "proxy_session")

	rec = visit(http.MethodPost, "/_dropsite/proxy/chat", `{"q":"hi"}`, "X-Dropsite-Session", g.Token)
	wantStatus(t, rec, http.StatusOK)
	if *auth != "Bearer sk-9" {
		t.Fatalf("upstream auth %q", *auth)
	}
	wantError(t, visit(http.MethodPost, "/_dropsite/proxy/chat", `{"q":"hi","x":1}`, "X-Dropsite-Session", g.Token),
		http.StatusBadRequest, "proxy_field")
	wantError(t, visit(http.MethodGet, "/_dropsite/session/challenge?purpose=form", ""), http.StatusNotFound, "not_found")
}
