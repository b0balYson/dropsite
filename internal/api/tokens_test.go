package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestTokens(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")

	rec := h.do(http.MethodPost, "/api/v1/tokens", tok, `{"name":"laptop","expires_in_days":30}`)
	wantStatus(t, rec, http.StatusCreated)
	created := decodeAs[newTokenDTO](t, rec)
	if !strings.HasPrefix(created.Value, "ds_") || created.Token.Name != "laptop" || created.Token.ExpiresAt == nil ||
		!created.Token.ExpiresAt.Equal(h.env.Clock.Now().Add(30*24*time.Hour)) || !strings.HasPrefix(created.Value, created.Token.Prefix) {
		t.Fatalf("created %+v", created)
	}
	// The new token works.
	wantStatus(t, h.do(http.MethodGet, "/api/v1/me", created.Value, nil), http.StatusOK)

	rec = h.do(http.MethodGet, "/api/v1/tokens", tok, nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), created.Value) || strings.Contains(rec.Body.String(), `"value"`) {
		t.Fatal("token value is listed")
	}
	l := decodeAs[list[tokenDTO]](t, rec)
	if l.Total != 2 || len(l.Items) != 2 {
		t.Fatalf("tokens %+v", l)
	}

	wantStatus(t, h.do(http.MethodDelete, "/api/v1/tokens/"+created.Token.ID, tok, nil), http.StatusNoContent)
	wantError(t, h.do(http.MethodGet, "/api/v1/me", created.Value, nil), http.StatusUnauthorized, domain.CodeUnauthorized)
	l = decodeAs[list[tokenDTO]](t, h.do(http.MethodGet, "/api/v1/tokens", tok, nil))
	for _, it := range l.Items {
		if it.ID == created.Token.ID && it.RevokedAt == nil {
			t.Fatal("revoked_at not set")
		}
	}
	wantError(t, h.do(http.MethodDelete, "/api/v1/tokens/abc", tok, nil), http.StatusNotFound, domain.CodeNotFound)
	wantError(t, h.do(http.MethodDelete, "/api/v1/tokens/999999", tok, nil), http.StatusNotFound, domain.CodeNotFound)
}

func TestCreateTokenValidation(t *testing.T) {
	h := newHarness(t)
	_, tok := h.user("bob@pp.test")
	for _, body := range []string{`{"name":"x","expires_in_days":0}`, `{"name":"x","expires_in_days":-1}`, `{"name":"x","expires_in_days":99999}`, `{"name":"x","expires_in_days":"7"}`} {
		wantError(t, h.do(http.MethodPost, "/api/v1/tokens", tok, body), http.StatusBadRequest, domain.CodeInvalidInput)
	}
	wantError(t, h.do(http.MethodPost, "/api/v1/tokens", tok, `{"name":""}`), http.StatusBadRequest, domain.CodeTokenInvalid)
	rec := h.do(http.MethodPost, "/api/v1/tokens", tok, `{"name":"forever"}`)
	wantStatus(t, rec, http.StatusCreated)
	if decodeAs[newTokenDTO](t, rec).Token.ExpiresAt != nil {
		t.Fatal("token without expiry got one")
	}
}
