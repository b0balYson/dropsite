package ui

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// byteLimitCodes carry a "limit" parameter in bytes.
var byteLimitCodes = map[string]bool{
	domain.CodeArchiveTooLarge:   true,
	domain.CodeArchiveFileTooBig: true,
}

// errorKey is the catalog key of an error code's message.
func errorKey(code string) string { return "err." + code }

// describe turns a service error into a translated message and HTTP status.
// Internal errors are logged and replaced by a generic text: their details
// never reach the browser.
func (s *server) describe(rc *reqCtx, err error) (string, int) {
	de := domain.AsError(err)
	status := statusOf(de.Kind)
	if de.Kind == domain.KindInternal || !rc.loc.Has(errorKey(de.Code)) {
		s.log.Error("ui request failed", "code", de.Code, "err", err)
		return rc.loc.T(errorKey(domain.CodeInternal)), http.StatusInternalServerError
	}
	return rc.loc.T(errorKey(de.Code), errorArgs(rc.loc, de)...), status
}

// errorArgs converts error parameters into placeholder values.
func errorArgs(l *locale, de *domain.Error) []any {
	keys := make([]string, 0, len(de.Params))
	for k := range de.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]any, 0, 2*len(keys)+2)
	for _, k := range keys {
		v := de.Params[k]
		switch {
		case k == "limit" && byteLimitCodes[de.Code]:
			v = l.Size(toInt64(v))
		case k == "key":
			v = l.T("settings." + fmt.Sprint(v))
		}
		args = append(args, k, v)
	}
	return append(args, "minutes", retryMinutes(de.RetryAfter))
}

func retryMinutes(d time.Duration) int {
	return max(1, int(math.Ceil(d.Minutes())))
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func statusOf(k domain.Kind) int {
	switch k {
	case domain.KindNotFound:
		return http.StatusNotFound
	case domain.KindInvalid:
		return http.StatusUnprocessableEntity
	case domain.KindConflict:
		return http.StatusConflict
	case domain.KindLimit:
		return http.StatusForbidden
	case domain.KindRateLimited:
		return http.StatusTooManyRequests
	case domain.KindUnauthorized:
		return http.StatusUnauthorized
	case domain.KindTooLarge:
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

// isNotFound reports whether err means "no such thing for this actor".
func isNotFound(err error) bool {
	return domain.AsError(err).Kind == domain.KindNotFound
}

// fail answers a page request with a service error: not found renders the
// 404 page, anything else a flash on the given page.
func (s *server) fail(w http.ResponseWriter, r *http.Request, rc *reqCtx, err error, back string) {
	if isNotFound(err) {
		s.notFound(w, r, rc)
		return
	}
	msg, _ := s.describe(rc, err)
	s.setFlash(w, r, flashError, msg)
	redirect(w, r, back)
}

// failPage renders the error page for errors on GET requests.
func (s *server) failPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, err error) {
	if isNotFound(err) {
		s.notFound(w, r, rc)
		return
	}
	msg, status := s.describe(rc, err)
	s.page(w, r, rc, status, "error", "", "error.generic.title",
		errorPage{Code: status, Heading: "error.generic.title", Message: msg})
}
