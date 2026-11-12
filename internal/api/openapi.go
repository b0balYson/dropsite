package api

import (
	_ "embed"
	"net/http"
)

// openAPIDocument is the hand-written OpenAPI 3.1 description of API v1.
// TestOpenAPIMatchesRoutes keeps it in sync with routes().
//
//go:embed openapi.json
var openAPIDocument []byte

func (a *api) openAPI(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPIDocument)
	return nil
}
