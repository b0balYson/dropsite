package api

import (
	"math"
	"net/http"
	"strconv"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Adapter-level errors. They reuse domain kinds and codes so every error the
// API writes goes through the same mapping.
var (
	errUnauthorized     = domain.ErrUnauthorized
	errRouteNotFound    = domain.ErrNotFound
	errMethodNotAllowed = domain.E(domain.KindInvalid, codeMethodNotAllowed, "method not allowed")
)

// codeMethodNotAllowed is the only code the adapter adds; it never reaches
// handlers because the router produces it.
const codeMethodNotAllowed = "method_not_allowed"

func invalid(message string) error {
	return domain.E(domain.KindInvalid, domain.CodeInvalidInput, message)
}

func invalidField(field, message string) error {
	return domain.E(domain.KindInvalid, domain.CodeInvalidInput, message).
		WithParams(map[string]any{"field": field})
}

func internalError() *domain.Error {
	return domain.E(domain.KindInternal, domain.CodeInternal, "internal error")
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

func errorBody(e *domain.Error) errorEnvelope {
	p := errorPayload{Code: e.Code, Message: e.Message}
	if e.Kind != domain.KindInternal && len(e.Params) > 0 {
		p.Details = e.Params
	}
	return errorEnvelope{Error: p}
}

// statusFor maps an error kind to its HTTP status (docs/api-contract.md).
func statusFor(e *domain.Error) int {
	if e.Code == codeMethodNotAllowed {
		return http.StatusMethodNotAllowed
	}
	if e.Code == domain.CodeRendererUnavailable {
		return http.StatusServiceUnavailable
	}
	switch e.Kind {
	case domain.KindNotFound:
		return http.StatusNotFound
	case domain.KindInvalid:
		return http.StatusBadRequest
	case domain.KindConflict:
		return http.StatusConflict
	case domain.KindLimit:
		return http.StatusForbidden
	case domain.KindTooLarge:
		return http.StatusRequestEntityTooLarge
	case domain.KindRateLimited:
		return http.StatusTooManyRequests
	case domain.KindUnauthorized:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// writeError writes err in the API format. Internal errors are logged with
// their cause and reach the client only as a generic message.
func (a *api) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := domain.AsError(err)
	if e.Kind == domain.KindInternal {
		a.logInternal(r, err)
		e = internalError()
	}
	status := statusFor(e)
	switch status {
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", retryAfterSeconds(e))
	}
	writeJSON(w, status, errorBody(e))
}

func (a *api) logInternal(r *http.Request, err error) {
	id := ""
	if info := infoFrom(r.Context()); info != nil {
		id = info.id
	}
	a.log.Error("api internal error", "request_id", id, "method", r.Method, "path", domain.RedactConnectLinks(r.URL.Path), "err", err)
}

// retryAfterSeconds rounds the hint up to whole seconds, at least one.
func retryAfterSeconds(e *domain.Error) string {
	secs := int64(math.Ceil(e.RetryAfter.Seconds()))
	return strconv.FormatInt(max(secs, 1), 10)
}
