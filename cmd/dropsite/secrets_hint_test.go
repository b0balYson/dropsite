package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSecretsRefusalHint(t *testing.T) {
	err := fmt.Errorf("deploy: %w", &client.APIError{
		Status: http.StatusBadRequest, Code: domain.CodeSecretsFound, Message: "the site contains secret keys",
		Details: map[string]any{"found": "js/pay.js:2 (Stripe secret key)", "count": float64(1)},
	})
	msg, hints := describeError(err)
	if !strings.Contains(msg, "secrets_found") {
		t.Fatalf("message: %q", msg)
	}
	joined := strings.Join(hints, "\n")
	for _, want := range []string{"found: js/pay.js:2 (Stripe secret key)", "count: 1", "--include-sensitive does not override"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hints lack %q: %s", want, joined)
		}
	}
}
