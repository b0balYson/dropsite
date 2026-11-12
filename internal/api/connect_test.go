package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestExchangeConnectCode(t *testing.T) {
	h := newHarness(t)
	u, _ := h.user("u@example.test")
	issued, err := h.env.Svc.CreateConnectCode(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	rec := h.do(http.MethodPost, "/api/v1/connect/exchange", "", map[string]string{"code": issued.Code, "name": "laptop"})
	wantStatus(t, rec, http.StatusCreated)
	res := decodeAs[exchangeResponse](t, rec)
	if res.User.Email != "u@example.test" || res.Token.Name != "laptop" || res.Value == "" {
		t.Fatalf("response: %+v", res)
	}
	wantStatus(t, h.do(http.MethodGet, "/api/v1/me", res.Value, nil), http.StatusOK)

	rec = h.do(http.MethodPost, "/api/v1/connect/exchange", "", map[string]string{"code": issued.Code})
	wantError(t, rec, http.StatusBadRequest, domain.CodeConnectInvalid)
	rec = h.do(http.MethodPost, "/api/v1/connect/exchange", "", map[string]string{})
	wantError(t, rec, http.StatusBadRequest, domain.CodeInvalidInput)
	rec = h.do(http.MethodGet, "/api/v1/connect/exchange", "", nil)
	wantStatus(t, rec, http.StatusMethodNotAllowed)
}

func TestExchangeConnectCodeRateLimited(t *testing.T) {
	h := newHarness(t)
	var rec *httptest.ResponseRecorder
	for range 11 {
		rec = h.do(http.MethodPost, "/api/v1/connect/exchange", "", map[string]string{"code": "AAAA-AAAA-AAAAA"})
	}
	wantError(t, rec, http.StatusTooManyRequests, domain.CodeRateLimited)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestMountsShareMiddleware(t *testing.T) {
	h := newHarness(t)
	var gotIP string
	mounted := New(h.env.Svc, nil, Mount{Pattern: "/mcp", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIP = RequestIP(r.Context())
		SetRequestUser(r.Context(), 7)
		w.WriteHeader(http.StatusTeapot)
	})})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()
	mounted.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot || gotIP != "203.0.113.9" || rec.Header().Get("X-Request-Id") == "" {
		t.Fatalf("mount: %d %q %v", rec.Code, gotIP, rec.Header())
	}
	if RequestIP(context.Background()) != "" {
		t.Fatal("ip outside middleware")
	}
	SetRequestUser(context.Background(), 1) // no-op outside middleware
}
