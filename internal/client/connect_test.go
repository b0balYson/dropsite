package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExchangeConnectCodeSendsNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/connect/exchange" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var in exchangeRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Code != "ABCD" || in.Name != "laptop" {
			t.Errorf("body %+v", in)
		}
		writeJSON(w, 201, map[string]any{"value": "ds_new", "user": map[string]any{"email": "a@b.c"}, "token": map[string]any{"name": "laptop"}})
	}))
	defer srv.Close()
	res, err := New(srv.URL, "").ExchangeConnectCode(context.Background(), "ABCD", "laptop")
	if err != nil || res.Value != "ds_new" || res.User.Email != "a@b.c" || res.Token.Name != "laptop" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestExchangeConnectCodeError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": "connect_code_invalid", "message": "bad"}})
	})
	if _, err := c.ExchangeConnectCode(context.Background(), "x", ""); err == nil {
		t.Fatal("no error")
	}
}
